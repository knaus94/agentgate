// What is read from an open Figma tab. Every function here runs inside the page.
//
// A viewer has no public API: the REST API and the MCP server are rate-limited for view seats, and the plugin
// API exists only with edit access. The web application itself, however, keeps the document in a scene graph
// that its own panels read, and a viewer who may export may export from it. These functions use that graph.
// It is not a published interface: when Figma changes it, `probe` reports which part is gone.

export function probe(nodeId) {
  if (/\/login|\/signup/.test(location.pathname)) {
    return { ready: false, fatal: true, reason: 'the Figma session has expired: sign in again with `figma login`' };
  }
  const app = window._fullscreen_;
  const mirror = app && app._store && app._store.getState().mirror;
  const graph = mirror && mirror.sceneGraph;
  if (!graph || typeof graph.get !== 'function') {
    return { ready: false, reason: 'the Figma application has not exposed its scene graph' };
  }
  const node = graph.get(nodeId);
  if (!node) {
    return { ready: false, reason: `node ${nodeId} is not in the loaded document` };
  }
  if (typeof node.getRestfulJSON !== 'function' || typeof node.loadImagesAndExport !== 'function') {
    return { ready: false, fatal: true, reason: 'the Figma application no longer offers the export calls this tool uses' };
  }
  // What a viewer may do is written in the file's own record. For a moment after the page opens the record
  // grants nothing at all, not even viewing: that is "not loaded yet", not a refusal.
  const state = app._store.getState();
  const file = state.openFile;
  if (!file || !file.canView) {
    return { ready: false, reason: 'the permissions of the file have not loaded' };
  }
  if (file.canExport === false) {
    return { ready: false, fatal: true, reason: 'the owner of this file does not let viewers copy or export it' };
  }
  return { ready: true, name: node.name, type: node.type, file: file.name, version: state.fileVersion };
}

// The node as the REST API would return it, and its renders.
export async function exportNode({ nodeId, scales, svg }) {
  const node = window._fullscreen_._store.getState().mirror.sceneGraph.get(nodeId);
  const base64 = (bytes) => {
    let text = '';
    for (let index = 0; index < bytes.length; index += 0x8000) {
      text += String.fromCharCode.apply(null, bytes.subarray(index, index + 0x8000));
    }
    return btoa(text);
  };
  const render = async (settings) => base64(await node.loadImagesAndExport({
    exportSettings: [{ suffix: '', contentsOnly: true, useAbsoluteBounds: false, colorProfile: 'DOCUMENT', useBicubicSampler: true, ...settings }],
  }));
  // The frame the node sits in is the width the screen was drawn for: the implementation is opened at that size.
  let top = node;
  while (top.parentNode && !['CANVAS', 'SECTION', 'DOCUMENT'].includes(top.parentNode.type)) top = top.parentNode;
  const out = { rest: node.getRestfulJSON(), png: {}, screen: { width: Math.round(top.size.x), height: Math.round(top.size.y) } };
  for (const scale of scales) {
    out.png[scale] = await render({ imageType: 'PNG', constraint: { type: 'CONTENT_SCALE', value: scale } });
  }
  if (svg) {
    out.svg = await render({ imageType: 'SVG', constraint: { type: 'CONTENT_SCALE', value: 1 }, svgIDMode: 'ALWAYS', svgOutlineText: false, svgDataName: true });
  }
  return out;
}

// Icons and pictures, each rendered by itself. A node that cannot be rendered is left out, not guessed.
export async function exportAssets({ rootId, requests }) {
  const graph = window._fullscreen_._store.getState().mirror.sceneGraph;
  // A layer inside a component instance is known to the export by a composed id, and to the graph by its own.
  const byExportId = new Map();
  const index = (node) => {
    byExportId.set(node.developerFriendlyId || node.guid, node);
    for (const child of node.childrenNodes || []) index(child);
  };
  index(graph.get(rootId));
  const base64 = (bytes) => {
    let text = '';
    for (let index = 0; index < bytes.length; index += 0x8000) {
      text += String.fromCharCode.apply(null, bytes.subarray(index, index + 0x8000));
    }
    return btoa(text);
  };
  const out = {};
  for (const request of requests) {
    const node = byExportId.get(request.id);
    if (!node) continue;
    const settings = request.format === 'svg'
      ? { imageType: 'SVG', constraint: { type: 'CONTENT_SCALE', value: 1 }, svgIDMode: 'IF_NEEDED', svgOutlineText: true }
      : { imageType: 'PNG', constraint: { type: 'CONTENT_SCALE', value: 2 } };
    try {
      out[request.id] = base64(await node.loadImagesAndExport({
        exportSettings: [{ suffix: '', contentsOnly: true, useAbsoluteBounds: false, colorProfile: 'DOCUMENT', useBicubicSampler: true, ...settings }],
      }));
    } catch {
      // not renderable by itself
    }
  }
  return out;
}

// Design tokens the node refers to: the name a designer gave a value is what maps it to a token in code.
export function readVariables(ids) {
  const graph = window._fullscreen_._store.getState().mirror.sceneGraph;
  const plain = (read) => {
    try {
      return JSON.parse(JSON.stringify(read()));
    } catch {
      return undefined;
    }
  };
  const out = {};
  for (const id of ids) {
    const variable = graph.get(id.replace(/^VariableID:/, ''));
    if (!variable || variable.type !== 'VARIABLE') continue;
    const values = plain(() => variable.variableDataValues);
    const collectionId = plain(() => variable.variableCollectionId);
    const collection = typeof collectionId === 'string' ? graph.get(collectionId.replace(/^VariableCollectionId:/, '')) : null;
    out[id] = { name: variable.name, collection: (collection && collection.name) || undefined,
      values: (values && values.values) || {}, code: plain(() => variable.variableCodeSyntax) };
  }
  return out;
}

// The frames of the page a node is on: what a lead chooses from before pulling anything.
export function listFrames(nodeId) {
  const graph = window._fullscreen_._store.getState().mirror.sceneGraph;
  const canvas = graph.get(graph.get(nodeId).containingCanvas);
  const rows = [];
  const add = (node, level) => rows.push({ level, id: node.guid, name: node.name, type: node.type.toLowerCase(),
    width: Math.round(node.size.x), height: Math.round(node.size.y) });
  for (const child of canvas.childrenNodes) {
    add(child, 0);
    if (child.type === 'SECTION') for (const inner of child.childrenNodes) add(inner, 1);
  }
  return { page: canvas.name, rows };
}

export function account() {
  const state = window._fullscreen_ && window._fullscreen_._store && window._fullscreen_._store.getState();
  const user = state && state.user;
  return user ? { email: user.email, name: user.name || user.handle } : null;
}
