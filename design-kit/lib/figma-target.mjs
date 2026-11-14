import { usageError } from './cli.mjs';

export function figmaTarget(value, node) {
  let parsed;
  try { parsed = new URL(value); } catch { throw usageError('provide a Figma file URL with a selected node'); }
  const key = /^\/(?:design|file|proto|board)\/([A-Za-z0-9]+)(?:\/|$)/.exec(parsed.pathname)?.[1];
  const id = String(node || parsed.searchParams.get('node-id') || '').replace(/^(\d+)-(\d+)$/, '$1:$2');
  if (parsed.protocol !== 'https:' || !/(^|\.)figma\.com$/.test(parsed.hostname) || parsed.username || parsed.password || !key || !/^\d+:\d+$/.test(id)) {
    throw usageError('copy a HTTPS Figma link to the selected frame (with node-id), or add --node <id>');
  }
  return { key, id, url: `https://www.figma.com/design/${key}/?node-id=${id.replace(':', '-')}` };
}

export function figmaTransport(flag, env = process.env) {
  const selected = flag || env.AGENTGATE_FIGMA_TRANSPORT || (env.FIGMA_ACCESS_TOKEN ? 'api' : 'browser');
  if (!['api', 'browser'].includes(selected)) throw usageError('transport must be api or browser');
  if (selected === 'api' && !env.FIGMA_ACCESS_TOKEN) throw usageError('API transport needs FIGMA_ACCESS_TOKEN with file_content:read scope');
  return selected;
}
