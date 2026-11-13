import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import http from 'node:http';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { openBrowser } from '../lib/browser.mjs';
import { listFrames, exportNode } from '../lib/figma-page.mjs';
import { serveFixtures } from '../lib/fixtures.mjs';

const check = fileURLToPath(new URL('../bin/check.mjs', import.meta.url));
const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

test('section catalogues expose content nodes without trying to render a section', async () => {
  const content = { guid: '3:1', name: 'Main Content', type: 'FRAME', size: { x: 800, y: 600 } };
  const frame = { guid: '2:1', name: 'Desktop', type: 'FRAME', size: { x: 1200, y: 900 }, childrenNodes: [content] };
  const section = { guid: '1:1', name: 'Partners', type: 'SECTION', containingCanvas: '0:1', childrenNodes: [frame] };
  const canvas = { guid: '0:1', name: 'Page', type: 'CANVAS', childrenNodes: [section] };
  const graph = new Map([section, frame, content, canvas].map((node) => [node.guid, node]));
  globalThis.window = { _fullscreen_: { _store: { getState: () => ({ mirror: { sceneGraph: graph } }) } } };
  try {
    const catalogue = listFrames({ nodeId: '1:1', depth: 3 });
    assert.deepEqual(catalogue.rows.map((row) => row.id), ['1:1', '2:1', '3:1']);
    assert.deepEqual(listFrames({ nodeId: '0:1', depth: 2 }).rows.map((row) => row.id), ['1:1', '2:1', '3:1']);
    const exported = await exportNode({ nodeId: '1:1', scales: [1] });
    assert.equal(exported.rest.document.type, 'SECTION');
    assert.deepEqual(exported.png, {});
  } finally {
    delete globalThis.window;
  }
});

test('serving fixtures never creates a missing directory in a read-only package', async (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'agentgate-fixtures-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const missing = path.join(dir, 'absent', 'fixtures');
  const server = await serveFixtures({ dir: missing, port: 0 });
  try {
    const response = await fetch(`http://127.0.0.1:${server.address().port}`, { method: 'POST', body: '{"query":"query Example { value }"}' });
    assert.equal(response.status, 200);
    assert.equal(fs.existsSync(missing), false);
  } finally {
    await new Promise((resolve) => server.close(resolve));
  }
});

test('real layout lifecycle: new page, overrides, baseline drift, errors and cancellation', { timeout: 120000 }, async (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'agentgate-layout-'));
  const screen = path.join(dir, 'screen');
  const pkg = path.join(screen, 'package');
  fs.mkdirSync(path.join(dir, '.git'));
  fs.mkdirSync(path.join(pkg, 'data'), { recursive: true });
  t.after(() => {
    const pid = path.join(dir, 'server.pid');
    if (fs.existsSync(pid)) {
      try { process.kill(Number(fs.readFileSync(pid, 'utf8')), 'SIGTERM'); } catch { /* already stopped */ }
    }
    fs.rmSync(dir, { recursive: true, force: true });
  });
  const listener = http.createServer();
  listener.listen(0, '127.0.0.1');
  await once(listener, 'listening');
  const port = listener.address().port;
  await new Promise((resolve) => listener.close(resolve));
  const url = `http://127.0.0.1:${port}`;
  const html = (width) => `<style>body{margin:0}#panel{width:${width}px;height:100px;background:#112233}</style><main id="panel"></main>`;
  const pageFile = path.join(dir, 'page.html');
  fs.writeFileSync(path.join(dir, 'server.mjs'), `import http from 'node:http'; import fs from 'node:fs';
fs.writeFileSync('server.pid', String(process.pid));
const server=http.createServer((req,res)=>{res.writeHead(200,{'content-type':'text/html'});res.end(fs.readFileSync('page.html'))});
server.listen(${port},'127.0.0.1');
process.on('SIGTERM',()=>server.close(()=>process.exit(0)));
`);
  fs.writeFileSync(path.join(dir, '.agentgate.json'), JSON.stringify({ design: { stand: { command: [process.execPath, 'server.mjs'], ready: url, ready_timeout_s: 5 } } }));
  fs.writeFileSync(path.join(screen, 'screen.json'), JSON.stringify({ url: '/page', root: '#old', viewport: '400x300' }));
  fs.writeFileSync(path.join(pkg, 'manifest.json'), JSON.stringify({ digest: 'design-v1', screen: { width: 400, height: 300 } }));
  fs.writeFileSync(path.join(pkg, 'data', 'spec.json'), JSON.stringify({ frame: { name: 'Panel', width: 200, height: 100 }, nodes: [
    { id: 'panel', ref: 'panel', name: 'Panel', box: [0, 0, 200, 100], fills: [{ color: '#112233' }], radius: 0 },
  ] }));
  // The fixture's reference is a known rectangle; production acceptance never blesses its own screenshot.
  const browser = await openBrowser();
  try {
    const page = await browser.newPage({ viewport: { width: 400, height: 300 } });
    await page.setContent(html(200));
    await page.locator('#panel').screenshot({ path: path.join(pkg, 'frame.png') });
  } finally {
    await browser.close();
  }
  const reachable = async () => {
    try { await fetch(url, { signal: AbortSignal.timeout(300) }); return true; } catch { return false; }
  };
  async function stopped() {
    for (let n = 0; n < 30 && await reachable(); n++) await pause(100);
    assert.equal(await reachable(), false, 'the task-owned stand survived a check');
    assert.equal(fs.existsSync(path.join(screen, 'fixtures')), false, 'layout created fixtures despite not recording');
  }
  async function run(args, expected) {
    const child = spawn(process.execPath, [check, screen, '--root', '#panel', '--url', '/page', ...args], { cwd: dir });
    let output = '';
    child.stdout.on('data', (data) => { output += data; });
    child.stderr.on('data', (data) => { output += data; });
    const [code] = await once(child, 'exit');
    assert.equal(code, expected, output);
    await stopped();
    return output;
  }
  const baseline = path.join(dir, 'baseline.json');
  fs.writeFileSync(pageFile, '<p>Page not built yet</p>');
  await run(['--write-baseline', baseline], 0);
  assert.deepEqual(JSON.parse(fs.readFileSync(baseline)).known, []);
  fs.writeFileSync(pageFile, html(200));
  const out = path.join(dir, 'report');
  await run(['--baseline', baseline, '--out', out], 0);
  assert.equal(JSON.parse(fs.readFileSync(path.join(out, 'report.json'))).issues.length, 0);
  assert.ok(fs.statSync(path.join(out, 'actual.png')).size > 0);
  fs.writeFileSync(pageFile, html(220));
  await run(['--baseline', baseline], 1);
  await run(['--write-baseline', baseline], 0);
  await run(['--baseline', baseline], 0);
  fs.writeFileSync(pageFile, html(240));
  await run(['--baseline', baseline], 1);
  fs.writeFileSync(pageFile, html(220));
  fs.writeFileSync(path.join(pkg, 'manifest.json'), JSON.stringify({ digest: 'design-v2' }));
  await run(['--baseline', baseline], 1);
  await run(['--baseline', path.join(dir, 'missing.json')], 1);
  await run(['--root', '#missing'], 2);
  const blockedOut = path.join(dir, 'not-a-directory');
  fs.writeFileSync(blockedOut, 'file');
  await run(['--out', blockedOut], 2);

  const child = spawn(process.execPath, [check, screen, '--root', '#panel', '--wait', '#never'], { cwd: dir, stdio: 'ignore' });
  const exit = once(child, 'exit');
  for (let n = 0; n < 80 && !(await reachable()); n++) await pause(100);
  assert.equal(await reachable(), true, 'the cancellation scenario never started its stand');
  child.kill('SIGTERM');
  const [code] = await exit;
  assert.equal(code, 143);
  await stopped();
});
