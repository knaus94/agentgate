import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { assetName, brief, buildSpec } from './outline.mjs';
import { replacePackage, writeJSON } from './files.mjs';

const sha256 = (data) => crypto.createHash('sha256').update(data).digest('hex');
const contentDigest = (files) => sha256(Object.keys(files).sort().map((name) => `${name}:${files[name]}`).join('\n'));

export function verifyPackage(dir, manifest) {
  if (!manifest || typeof manifest.digest !== 'string' || !manifest.digest) throw new Error('design package has no content digest');
  if (!manifest.files || typeof manifest.files !== 'object' || Array.isArray(manifest.files)) throw new Error('design package inventory is missing or invalid; import the design again');
  for (const [name, expected] of Object.entries(manifest.files)) {
    if (path.isAbsolute(name) || name.split(/[\\/]/).some((part) => part === '..' || part === '') || !/^[a-f0-9]{64}$/.test(expected)) {
      throw new Error(`invalid package entry: ${name}`);
    }
    const target = path.join(dir, name);
    const real = fs.realpathSync(target);
    const root = fs.realpathSync(dir) + path.sep;
    if (!real.startsWith(root) || !fs.lstatSync(target).isFile() || sha256(fs.readFileSync(target)) !== expected) {
      throw new Error(`design package was modified or is incomplete: ${name}; import it again`);
    }
  }
  for (const required of ['frame.png', 'data/spec.json', 'data/nodes.json', 'brief.md']) {
    if (!Object.hasOwn(manifest.files, required)) throw new Error(`package inventory omits ${required}`);
  }
  if (contentDigest(manifest.files) !== manifest.digest) throw new Error('design package digest does not match its inventory');
}

export async function exportPackage({ destination, where, state, data, scales = [1], variables = {}, renderAssets, includeAssets = true, transport = 'browser' }) {
  const spec = buildSpec(data.rest, variables);
  return replacePackage(destination, async (out) => {
    fs.mkdirSync(path.join(out, 'assets'));
    fs.mkdirSync(path.join(out, 'data'));
    const files = {};
    const write = (name, content) => {
      fs.writeFileSync(path.join(out, name), content);
      files[name] = sha256(content);
    };
    if (includeAssets) {
      const wanted = spec.nodes.filter((node) => node.asset);
      if (wanted.length > 300) throw new Error('design has more than 300 assets; import a smaller frame or use --no-assets');
      const written = new Map();
      for (let index = 0; index < wanted.length; index += 25) {
        const batch = wanted.slice(index, index + 25);
        const rendered = await renderAssets(batch.map((node) => ({ id: node.id, format: node.asset.format })));
        for (const node of batch) {
          if (!rendered[node.id]) throw new Error(`Figma did not render asset ${node.name} (${node.id}); package was not replaced`);
          const bytes = Buffer.from(rendered[node.id], 'base64');
          const digest = sha256(bytes);
          if (!written.has(digest)) {
            const name = `assets/${assetName(node)}`;
            written.set(digest, name);
            write(name, bytes);
          }
          node.asset.file = written.get(digest);
        }
      }
    }
    for (const scale of scales) {
      if (!data.png[scale]) throw new Error(`Figma did not render the frame at scale ${scale}`);
      write(scale === 1 ? 'frame.png' : `data/frame@${scale}x.png`, Buffer.from(data.png[scale], 'base64'));
    }
    if (data.context) write('context.png', Buffer.from(data.context, 'base64'));
    write('data/nodes.json', JSON.stringify(data.rest));
    write('data/variables.json', JSON.stringify(variables));
    write('data/spec.json', JSON.stringify(spec));
    const text = brief(spec, `Figma “${state.file}”, node ${where.id}, file version ${state.version}`, destination);
    write('brief.md', text);
    const manifest = { tool: 'agentgate-design 1', transport,
      source: { url: where.url, file: state.file, key: where.key, node: where.id, version: state.version },
      frame: spec.frame, screen: data.screen, pulled_at: new Date().toISOString(), digest: contentDigest(files), files };
    writeJSON(path.join(out, 'manifest.json'), manifest);
    verifyPackage(out, manifest);
    return { package: destination, frame: spec.frame, layers: spec.nodes.length, transport,
      assets: Object.keys(files).filter((name) => name.startsWith('assets/')).length,
      brief: { file: path.join(destination, 'brief.md'), lines: text.split('\n').length, tokens_about: Math.round(Buffer.byteLength(text) / 3.2) }, digest: manifest.digest };
  });
}
