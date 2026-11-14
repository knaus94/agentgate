// Documented Figma REST endpoints. Tokens are used only for api.figma.com, never for asset downloads.
import { httpURL } from './cli.mjs';

async function bytes(response, limit = 64 * 1024 * 1024) {
  if (Number(response.headers.get('content-length')) > limit) throw new Error('Figma response exceeds the 64 MiB limit; select a smaller node');
  if (!response.body) return Buffer.alloc(0);
  const reader = response.body.getReader();
  const chunks = [];
  let size = 0;
  try {
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > limit) throw new Error('Figma response exceeds the 64 MiB limit; select a smaller node');
      chunks.push(Buffer.from(value));
    }
    return Buffer.concat(chunks);
  } finally { await reader.cancel().catch(() => {}); }
}

export class FigmaAPI {
  constructor({ token = process.env.FIGMA_ACCESS_TOKEN, fetchImpl = fetch } = {}) {
    if (!token) throw new Error('FIGMA_ACCESS_TOKEN is required for API transport');
    this.token = token;
    this.fetch = fetchImpl;
  }

  async request(endpoint, query = {}) {
    const url = new URL(`https://api.figma.com/v1/${endpoint}`);
    for (const [name, value] of Object.entries(query)) if (value !== undefined) url.searchParams.set(name, String(value));
    const response = await this.fetch(url, { headers: { 'X-Figma-Token': this.token }, redirect: 'error', signal: AbortSignal.timeout(60000) });
    if (!response.ok) {
      await response.body?.cancel();
      const retry = response.headers.get('retry-after');
      if (response.status === 429) throw new Error(`Figma API rate limit reached${retry ? `; retry after ${retry} seconds` : ''}; no automatic retry or transport switch was attempted`);
      if ([401, 403].includes(response.status)) throw new Error(`Figma API HTTP ${response.status}: check token expiry, file_content:read scope and file access`);
      throw new Error(`Figma API returned HTTP ${response.status}`);
    }
    return JSON.parse((await bytes(response)).toString('utf8'));
  }

  async document(where) {
    const file = await this.request(`files/${encodeURIComponent(where.key)}`, { ids: where.id });
    const find = (node, parents = []) => {
      if (!node) return null;
      if (node.id === where.id) return { node, parents };
      for (const child of node.children || []) {
        const hit = find(child, [...parents, node]);
        if (hit) return hit;
      }
      return null;
    };
    const found = find(file.document);
    if (!found) throw new Error(`Figma node ${where.id} was not found in this file`);
    return { ...found, file, state: { file: file.name, version: file.version },
      rest: { document: found.node, components: file.components, componentSets: file.componentSets, styles: file.styles } };
  }

  catalogue(document, depth = 1) {
    const rows = [];
    const walk = (node, level) => {
      if (rows.length >= 500 || level > depth || node.visible === false) return;
      rows.push({ id: node.id, name: node.name, type: String(node.type).toLowerCase(), level,
        width: Math.round(node.absoluteBoundingBox?.width || 0), height: Math.round(node.absoluteBoundingBox?.height || 0) });
      for (const child of node.children || []) walk(child, level + 1);
    };
    walk(document.node, 0);
    return { page: document.parents.find((node) => node.type === 'CANVAS')?.name || document.file.name, rows, truncated: rows.length >= 500 };
  }

  async render(where, ids, { format = 'png', scale = 1, version, absolute = true } = {}) {
    if (!ids.length) return {};
    const result = await this.request(`images/${encodeURIComponent(where.key)}`, { ids: ids.join(','), format, scale, version, use_absolute_bounds: absolute });
    const images = {};
    // Bounded concurrency avoids hundreds of simultaneous CDN requests for a large frame.
    for (let index = 0; index < ids.length; index += 4) {
      await Promise.all(ids.slice(index, index + 4).map(async (id) => {
        const source = result.images?.[id];
        if (!source) throw new Error(`Figma could not render node ${id}`);
        const url = httpURL(source, 'Figma image URL');
        if (url.protocol !== 'https:') throw new Error('Figma returned an insecure image URL');
        const response = await this.fetch(url, { signal: AbortSignal.timeout(60000) });
        if (!response.ok) { await response.body?.cancel(); throw new Error(`Figma image download returned HTTP ${response.status}`); }
        images[id] = (await bytes(response)).toString('base64');
      }));
    }
    return images;
  }

  async export(where, document, scales) {
    const box = document.node.absoluteBoundingBox;
    if (!box || ['CANVAS', 'DOCUMENT', 'SECTION'].includes(document.node.type)) throw new Error('select a frame or component, not a page or section; use figma frames to list children');
    const ancestors = [...document.parents, document.node];
    let top = document.node;
    for (let index = ancestors.length - 2; index >= 0; index--) {
      if (['CANVAS', 'DOCUMENT', 'SECTION'].includes(ancestors[index].type)) break;
      top = ancestors[index];
    }
    const origin = top.absoluteBoundingBox || box;
    const data = { rest: document.rest, png: {}, screen: { id: top.id, name: top.name,
      width: Math.round(origin.width), height: Math.round(origin.height),
      root: { x: Math.round(box.x - origin.x), y: Math.round(box.y - origin.y), width: Math.round(box.width), height: Math.round(box.height) } } };
    for (const scale of scales) {
      const ids = scale === 1 && top.id !== where.id ? [where.id, top.id] : [where.id];
      const images = await this.render(where, ids, { scale, version: document.file.version });
      data.png[scale] = images[where.id];
      if (scale === 1 && top.id !== where.id) data.context = images[top.id];
    }
    return data;
  }

  async assets(where, requests, version) {
    const out = {};
    for (const format of ['png', 'svg']) {
      const ids = requests.filter((request) => request.format === format).map((request) => request.id);
      Object.assign(out, await this.render(where, ids, { format, scale: format === 'png' ? 2 : 1, version, absolute: false }));
    }
    return out;
  }
}
