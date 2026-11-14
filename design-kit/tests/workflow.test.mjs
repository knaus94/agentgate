import assert from 'node:assert/strict';
import crypto from 'node:crypto';
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
import { readPage, diffImages } from '../lib/page-read.mjs';
import { compare } from '../lib/compare.mjs';

const check = fileURLToPath(new URL('../bin/check.mjs', import.meta.url));
const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// Test packages use the same complete inventory contract as actual Figma exports.
function sealTestPackage(dir) {
  const manifest = JSON.parse(fs.readFileSync(path.join(dir, 'manifest.json')));
  fs.writeFileSync(path.join(dir, 'brief.md'), `Test design ${manifest.versionTag}\n`);
  if (!fs.existsSync(path.join(dir, 'data', 'nodes.json'))) fs.writeFileSync(path.join(dir, 'data', 'nodes.json'), '{}');
  const hash = (data) => crypto.createHash('sha256').update(data).digest('hex');
  const files = Object.fromEntries(['brief.md', 'data/nodes.json', 'data/spec.json', 'frame.png'].map((name) =>
    [name, hash(fs.readFileSync(path.join(dir, name)))]));
  const digest = hash(Object.keys(files).sort().map((name) => `${name}:${files[name]}`).join('\n'));
  fs.writeFileSync(path.join(dir, 'manifest.json'), JSON.stringify({ ...manifest, files, digest }));
}

test('layout measurements do not hide unpainted root geometry or an absent border', async (t) => {
  const spec = { frame: { width: 200, height: 100 }, nodes: [
    { id: 'root', ref: 'root', name: 'Content', box: [0, 0, 200, 100] },
    { id: 'field', ref: 'field', parent: 'root', name: 'Promo field', box: [0, 0, 200, 50],
      fills: [{ color: '#112233', opacity: 1 }], stroke: { weight: 1, paints: [{ color: '#FFFFFF', opacity: 1 }] } },
  ] };
  const page = { root: { width: 200, height: 124 }, texts: [], boxes: [
    { where: 'span', tag: 'span', painted: true, box: [0, 0, 200, 50], background: { r: 17, g: 34, b: 51, a: 1 }, border: null },
  ] };
  const result = compare(spec, page);
  await t.test('the selected root still has a measurable height without a fill', () => {
    assert.ok(result.issues.some((issue) => issue.ref === 'root' && issue.kind === 'size'));
  });
  await t.test('a missing border differs from the reference border', () => {
    assert.ok(result.issues.some((issue) => issue.ref === 'field' && issue.kind === 'border'));
  });
});

test('explicit CSS mappings keep overlapping artwork distinct and expose real offsets', async () => {
  const browser = await openBrowser();
  try {
    const page = await browser.newPage();
    await page.setContent('<style>#root{position:relative;width:400px;height:400px}img{position:absolute;width:40px;height:40px;top:0}[data-kind=front]{left:150px}[data-kind=back]{left:0}</style><main id="root"><img data-kind="front"><img data-kind="back"></main>');
    const select = { front: '[data-kind="front"]', back: '[data-kind="back"]' };
    const seen = await page.evaluate(readPage, { root: '#root', select });
    assert.equal(seen.error, undefined);
    const spec = { nodes: ['front', 'back'].map((ref) => ({ id: ref, ref, name: ref, asset: { format: 'png' }, box: [0, 0, 40, 40] })) };
    const result = compare(spec, seen, { select });
    assert.equal(result.compared, 2);
    assert.equal(result.issues.some((issue) => issue.kind === 'missing'), false);
    assert.ok(result.issues.some((issue) => issue.ref === 'front' && issue.kind === 'position'));
    const ambiguous = await page.evaluate(readPage, { root: '#root', select: { front: 'img' } });
    assert.match(ambiguous.error, /more than one/);
    const pictures = await page.evaluate(() => {
      const png = (width, height) => {
        const canvas = document.createElement('canvas');
        canvas.width = width; canvas.height = height;
        const context = canvas.getContext('2d');
        context.fillStyle = '#112233'; context.fillRect(0, 0, width, height);
        return canvas.toDataURL();
      };
      return {
        design: png(100, 100), actual: png(100, 50), crop: { x: 0, y: 0, width: 100, height: 100 }, marks: [],
      };
    });
    const pixels = await page.evaluate(diffImages, pictures);
    assert.ok(pixels.ratio >= 0.5, 'missing half of the image must not be cropped out of the difference');
  } finally {
    await browser.close();
  }
});

test('modern CSS colours remain measurable as painted sRGB boxes', async () => {
  const browser = await openBrowser();
  try {
    const page = await browser.newPage();
    await page.setContent('<main id="root"><div id="oklch" style="width:40px;height:40px;background:oklch(0.5 0 0)"></div><div id="p3" style="width:40px;height:40px;background:color(display-p3 1 0 0 / 0.5)"></div></main>');
    const seen = await page.evaluate(readPage, '#root');
    const grey = seen.boxes.find((box) => box.where === 'div#oklch');
    assert.equal(grey.painted, true);
    assert.ok(grey.background.r >= 98 && grey.background.r <= 100);
    assert.equal(grey.background.r, grey.background.g);
    assert.equal(grey.background.g, grey.background.b);
    const red = seen.boxes.find((box) => box.where === 'div#p3');
    assert.equal(red.painted, true);
    assert.equal(red.background.r, 255);
    assert.equal(red.background.g, 0);
    assert.equal(red.background.b, 0);
    assert.ok(Math.abs(red.background.a - 0.5) < 0.01);
  } finally {
    await browser.close();
  }
});

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
    const renders = [];
    for (const [node, parent, bounds] of [
      [frame, section, { x: 100, y: 200, width: 1200, height: 900 }],
      [content, frame, { x: 120, y: 328, width: 800, height: 600 }],
    ]) {
      node.parentNode = parent;
      node.absoluteBoundingBox = bounds;
      node.getRestfulJSON = () => ({ document: { id: node.guid, absoluteBoundingBox: bounds } });
      node.loadImagesAndExport = async ({ exportSettings }) => {
        renders.push({ id: node.guid, absolute: exportSettings[0].useAbsoluteBounds });
        return new TextEncoder().encode(node.guid);
      };
    }
    const selected = await exportNode({ nodeId: content.guid, scales: [1] });
    assert.equal(selected.rest.document.id, content.guid);
    assert.equal(selected.screen.id, frame.guid);
    assert.deepEqual(selected.screen.root, { x: 20, y: 128, width: 800, height: 600 });
    assert.equal(atob(selected.context), frame.guid);
    assert.deepEqual(renders, [{ id: content.guid, absolute: true }, { id: frame.guid, absolute: true }]);
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
  const mobileHTML = '<meta name="viewport" content="width=device-width,initial-scale=1"><style>*{box-sizing:border-box}html{scrollbar-gutter:stable}body{margin:0}main{width:100%;padding:0 12px;background:#112233}section{width:100%;height:100px}</style><main id="shell"><section id="panel"></section></main>';
  const pageFile = path.join(dir, 'page.html');
  fs.writeFileSync(path.join(dir, 'server.mjs'), `import http from 'node:http'; import fs from 'node:fs';
fs.writeFileSync('server.pid', String(process.pid));
const server=http.createServer((req,res)=>{res.writeHead(200,{'content-type':'text/html'});res.end(fs.readFileSync('page.html'))});
server.listen(${port},'127.0.0.1');
process.on('SIGTERM',()=>server.close(()=>process.exit(0)));
`);
  fs.writeFileSync(path.join(dir, '.agentgate.json'), JSON.stringify({ design: { stand: { command: [process.execPath, 'server.mjs'], ready: url, ready_timeout_s: 5 } } }));
  fs.writeFileSync(path.join(screen, 'screen.json'), JSON.stringify({ url: '/page', root: '#old', viewport: '400x300' }));
  fs.writeFileSync(path.join(pkg, 'manifest.json'), JSON.stringify({ versionTag: 'design-v1', screen: { width: 400, height: 300 } }));
  fs.writeFileSync(path.join(pkg, 'data', 'spec.json'), JSON.stringify({ frame: { name: 'Panel', width: 200, height: 100 }, nodes: [
    { id: 'panel', ref: 'panel', name: 'Panel', box: [0, 0, 200, 100], fills: [{ color: '#112233' }], radius: 0 },
  ] }));
  // The fixture's reference is a known rectangle; production acceptance never blesses its own screenshot.
  const browser = await openBrowser();
  try {
    const page = await browser.newPage({ viewport: { width: 400, height: 300 } });
    await page.setContent(html(200));
    await page.locator('#panel').screenshot({ path: path.join(pkg, 'frame.png') });
    const mobile = await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, deviceScaleFactor: 1 });
    const mobilePage = await mobile.newPage();
    await mobilePage.setContent(mobileHTML);
    await mobilePage.locator('#shell').screenshot({ path: path.join(dir, 'mobile.png') });
    await mobilePage.setViewportSize({ width: 768, height: 1024 });
    await mobilePage.locator('#shell').screenshot({ path: path.join(dir, 'tablet.png') });
    await mobile.close();
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
    sealTestPackage(pkg);
    const defaults = [...(args.includes('--root') ? [] : ['--root', '#panel']), ...(args.includes('--url') ? [] : ['--url', '/page'])];
    const child = spawn(process.execPath, [check, screen, ...defaults, ...args], { cwd: dir });
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
  assert.ok(fs.statSync(path.join(out, 'page.png')).size > 0);
  fs.writeFileSync(pageFile, html(220));
  await run(['--baseline', baseline], 1);
  await run(['--write-baseline', baseline], 0);
  await run(['--baseline', baseline], 0);
  fs.writeFileSync(pageFile, html(240));
  await run(['--baseline', baseline], 1);
  fs.writeFileSync(pageFile, html(220));
  fs.writeFileSync(path.join(pkg, 'manifest.json'), JSON.stringify({ versionTag: 'design-v2' }));
  await run(['--baseline', baseline], 1);
  await run(['--baseline', path.join(dir, 'missing.json')], 1);
  assert.match(await run(['--root', '#missing'], 2), /no element matches #missing/);
  const blockedOut = path.join(dir, 'not-a-directory');
  fs.writeFileSync(blockedOut, 'file');
  await run(['--out', blockedOut], 2);

  fs.writeFileSync(pageFile, mobileHTML);
  fs.writeFileSync(path.join(pkg, 'manifest.json'), JSON.stringify({ versionTag: 'mobile-v1', screen: { width: 390, height: 844 } }));
  fs.writeFileSync(path.join(pkg, 'data', 'spec.json'), JSON.stringify({ frame: { name: 'Mobile content', width: 390, height: 100 }, nodes: [
    { id: 'shell', ref: 'shell', name: 'Content including padding', box: [0, 0, 390, 100], fills: [{ color: '#112233' }], radius: 0 },
  ] }));
  fs.copyFileSync(path.join(dir, 'mobile.png'), path.join(pkg, 'frame.png'));
  await run(['--root', '#shell', '--viewport', '390x844'], 1);
  await run(['--root', '#shell', '--viewport', '390x844', '--device', 'mobile', '--out', out], 0);
  const measurement = JSON.parse(fs.readFileSync(path.join(out, 'report.json'))).measurement;
  assert.equal(measurement.device, 'mobile');
  assert.equal(measurement.documentWidth, 390);
  assert.equal(measurement.root.width, 390);
  assert.deepEqual(measurement.root.padding, [0, 12, 0, 12]);
  // A narrow child is not equivalent to the padded frame, even in a correctly emulated mobile browser.
  const wrongRoot = await run(['--viewport', '390x844', '--device', 'mobile'], 1);
  assert.match(wrongRoot, /root #panel 366x100/);
  const mapping = path.join(dir, 'ambiguous-map.json');
  fs.writeFileSync(mapping, JSON.stringify({ select: { shell: 'main, section' } }));
  await run(['--root', '#shell', '--device', 'mobile', '--map', mapping, '--write-baseline', baseline], 2);
  fs.writeFileSync(path.join(pkg, 'manifest.json'), JSON.stringify({ versionTag: 'tablet-v1', screen: { width: 768, height: 1024 } }));
  fs.writeFileSync(path.join(pkg, 'data', 'spec.json'), JSON.stringify({ frame: { name: 'Tablet content', width: 768, height: 100 }, nodes: [
    { id: 'shell', ref: 'shell', name: 'Content including padding', box: [0, 0, 768, 100], fills: [{ color: '#112233' }], radius: 0 },
  ] }));
  fs.copyFileSync(path.join(dir, 'tablet.png'), path.join(pkg, 'frame.png'));
  await run(['--root', '#shell', '--viewport', '768x1024', '--device', 'mobile', '--out', out], 0);
  assert.equal(JSON.parse(fs.readFileSync(path.join(out, 'report.json'))).measurement.root.width, 768);

  const child = spawn(process.execPath, [check, screen, '--root', '#panel', '--wait', '#never'], { cwd: dir, stdio: 'ignore' });
  const exit = once(child, 'exit');
  for (let n = 0; n < 80 && !(await reachable()); n++) await pause(100);
  assert.equal(await reachable(), true, 'the cancellation scenario never started its stand');
  child.kill('SIGTERM');
  const [code] = await exit;
  assert.equal(code, 143);
  await stopped();
});
