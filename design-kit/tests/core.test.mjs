import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import http from 'node:http';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { parseArgs } from '../lib/cli.mjs';
import { serveFixtures, fixtureIdentity } from '../lib/fixtures.mjs';
import { replacePackage } from '../lib/files.mjs';
import { exportPackage, verifyPackage } from '../lib/package.mjs';
import { figmaTarget, figmaTransport } from '../lib/figma-target.mjs';
import { FigmaAPI } from '../lib/figma-api.mjs';
import { compare } from '../lib/compare.mjs';
import { ensureStand, stopProcess } from '../lib/stand.mjs';

const bin = (name) => fileURLToPath(new URL(`../bin/${name}.mjs`, import.meta.url));
const temp = (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'agentgate-design-unit-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
};
const close = (server) => new Promise((resolve) => { server.close(resolve); server.closeAllConnections(); });
const post = (server, body, headers = {}) => fetch(`http://127.0.0.1:${server.address().port}/graphql`, {
  method: 'POST', headers: { 'content-type': 'application/json', ...headers }, body: typeof body === 'string' ? body : JSON.stringify(body),
});

test('all design commands show help without touching files or starting a process', (t) => {
  const dir = temp(t);
  for (const [script, args] of [['figma', ['login', '--help']], ['figma', ['pull', '--help']],
    ['check', ['new', 'screen', '--help']], ['graphql-fixtures', ['fixtures', '--help']]]) {
    const result = spawnSync(process.execPath, [bin(script), ...args], { cwd: dir, encoding: 'utf8', timeout: 3000,
      env: { ...process.env, AGENTGATE_FIGMA_HOME: path.join(dir, 'private'), DISPLAY: '', WAYLAND_DISPLAY: '' } });
    assert.equal(result.status, 0, result.stderr);
    assert.match(result.stdout, /usage:/);
  }
  assert.deepEqual(fs.readdirSync(dir), []);
});

test('design argument errors are rejected before side effects', (t) => {
  const dir = temp(t);
  for (const [script, args] of [['figma', ['pull', 'https://figma.com/design/abc/?node-id=1-2', '--out', 'package', '--typo']],
    ['check', ['package', '--tolerance', 'NaN']], ['check', ['package', '--max-pixel-diff', '2']],
    ['graphql-fixtures', ['fixtures', '--port', '-2']], ['graphql-fixtures', ['fixtures', '--record']]]) {
    const result = spawnSync(process.execPath, [bin(script), ...args], { cwd: dir, encoding: 'utf8', timeout: 3000 });
    assert.equal(result.status, 2, `${script}: ${result.stderr}`);
  }
  assert.deepEqual(fs.readdirSync(dir), []);
  assert.throws(() => parseArgs(['--out', 'a', '--out', 'b'], { values: ['out'] }), /duplicate/);
  assert.deepEqual(parseArgs(['--out=a', '--', '--literal'], { values: ['out'] }).rest, ['--literal']);
});

test('GraphQL names cannot read or overwrite files outside the fixture directory', async (t) => {
  const dir = temp(t);
  const sentinel = path.join(dir, 'outside.json');
  fs.writeFileSync(sentinel, '{"secret":"sentinel"}');
  const fixtureDir = path.join(dir, 'fixtures');
  fs.mkdirSync(fixtureDir);
  const server = await serveFixtures({ dir: fixtureDir, port: 0 });
  t.after(() => close(server));
  for (const name of ['../outside', '/outside', 'foo/bar', '__bad-name']) {
    const response = await post(server, { operationName: name });
    assert.equal(response.status, 400);
    assert.match((await response.json()).errors[0].message, /GraphQL name/);
  }
  fs.symlinkSync(sentinel, path.join(fixtureDir, 'Read.json'));
  assert.equal((await post(server, { operationName: 'Read' })).status, 500);
  assert.equal(fs.readFileSync(sentinel, 'utf8'), '{"secret":"sentinel"}');
});

test('fixtures make missing data, malformed input and untrusted origins visible', async (t) => {
  const server = await serveFixtures({ dir: path.join(temp(t), 'missing'), port: 0, maxBodyBytes: 128 });
  t.after(() => close(server));
  assert.equal((await (await post(server, { operationName: 'Missing' })).json()).errors[0].extensions.code, 'FIXTURE_NOT_FOUND');
  assert.equal((await post(server, '{broken')).status, 400);
  assert.equal((await post(server, null)).status, 400);
  assert.equal((await post(server, [])).status, 400);
  assert.equal((await post(server, { variables: [] })).status, 400);
  assert.equal((await post(server, 'x'.repeat(200))).status, 413);
  assert.equal((await post(server, {}, { origin: 'https://untrusted.example' })).status, 403);
  const local = await post(server, {}, { origin: 'http://localhost:3000' });
  assert.equal(local.headers.get('access-control-allow-origin'), 'http://localhost:3000');
});

test('fixture identity is stable across variable key order and each recording replays exactly', async (t) => {
  const dir = temp(t);
  const a = { operationName: 'User', variables: { b: 2, a: 1 } };
  assert.equal(fixtureIdentity(a).digest, fixtureIdentity({ ...a, variables: { a: 1, b: 2 } }).digest);
  let responseValue = 'first';
  const record = await serveFixtures({ dir, port: 0, record: 'https://example.test/graphql', fetchImpl: async () => Response.json({ data: responseValue }) });
  await post(record, a);
  responseValue = 'second';
  await post(record, { ...a, variables: { id: 2 } });
  responseValue = 'updated';
  await post(record, a);
  await close(record);
  const replay = await serveFixtures({ dir, port: 0 });
  t.after(() => close(replay));
  assert.deepEqual(await (await post(replay, a)).json(), { data: 'updated' });
  assert.deepEqual(await (await post(replay, { ...a, variables: { id: 2 } })).json(), { data: 'second' });
  const missing = await (await post(replay, { ...a, variables: { id: 'never-recorded' } })).json();
  assert.equal(missing.errors[0].extensions.code, 'FIXTURE_NOT_FOUND');
  assert.equal(fs.existsSync(path.join(dir, 'User.json')), false, 'recording must not invent an all-variables fallback');
});

test('upstream HTTP errors never become valid recordings', async (t) => {
  const dir = temp(t);
  const server = await serveFixtures({ dir, port: 0, record: 'https://example.test/graphql', fetchImpl: async () => new Response('unavailable', { status: 503 }) });
  t.after(() => close(server));
  assert.equal((await post(server, { operationName: 'User' })).status, 502);
  assert.deepEqual(fs.readdirSync(dir), []);
});

test('failed and concurrent exports preserve the last complete design package', async (t) => {
  const dir = path.join(temp(t), 'package');
  fs.mkdirSync(dir);
  fs.writeFileSync(path.join(dir, 'manifest.json'), '{"digest":"old"}');
  fs.writeFileSync(path.join(dir, 'frame.png'), 'old');
  await assert.rejects(replacePackage(dir, async (staging) => {
    fs.writeFileSync(path.join(staging, 'frame.png'), 'new');
    await assert.rejects(replacePackage(dir, async () => {}), /another export/);
    throw new Error('download failed');
  }), /download failed/);
  assert.equal(fs.readFileSync(path.join(dir, 'frame.png'), 'utf8'), 'old');
  assert.equal(fs.existsSync(`${dir}.export.lock`), false);
});

test('export setup failures release their own lock and descriptor', async (t) => {
  const root = temp(t);
  const dir = path.join(root, 'package');
  const create = t.mock.method(fs, 'mkdtempSync', () => { throw new Error('staging unavailable'); });
  await assert.rejects(replacePackage(dir, async () => {}), /staging unavailable/);
  assert.equal(fs.existsSync(`${dir}.export.lock`), false);
  create.mock.restore();
  const write = fs.writeFileSync;
  let descriptor;
  const failed = t.mock.method(fs, 'writeFileSync', (file, ...args) => {
    if (typeof file === 'number') { descriptor = file; throw new Error('lock write failed'); }
    return write(file, ...args);
  });
  await assert.rejects(replacePackage(dir, async () => {}), /lock write failed/);
  assert.equal(fs.existsSync(`${dir}.export.lock`), false);
  assert.throws(() => fs.fstatSync(descriptor), { code: 'EBADF' });
  failed.mock.restore();
  await replacePackage(dir, async (staging) => fs.writeFileSync(path.join(staging, 'manifest.json'), '{}'));
  assert.equal(fs.existsSync(path.join(dir, 'manifest.json')), true);
});

test('cleanup cannot adopt an arbitrary or recycled child PID', async (t) => {
  const kill = t.mock.method(process, 'kill', () => { throw new Error('unowned process was signalled'); });
  await stopProcess({ pid: process.pid, exitCode: null, signalCode: null });
  await stopProcess({ pid: process.pid, exitCode: 0, signalCode: null });
  assert.equal(kill.mock.callCount(), 0);
});

test('exported inventories detect tampering instead of accepting stale design evidence', async (t) => {
  const dir = path.join(temp(t), 'package');
  const data = { rest: { document: { id: '1:2', name: 'Frame', type: 'FRAME', absoluteBoundingBox: { x: 0, y: 0, width: 10, height: 10 }, children: [] } }, png: { 1: Buffer.from('png fixture').toString('base64') } };
  await exportPackage({ destination: dir, where: { id: '1:2', key: 'example' }, state: { file: 'Example', version: 'v1' }, data, renderAssets: async () => ({}) });
  const manifest = JSON.parse(fs.readFileSync(path.join(dir, 'manifest.json')));
  verifyPackage(dir, manifest);
  fs.writeFileSync(path.join(dir, 'data', 'spec.json'), '{}');
  assert.throws(() => verifyPackage(dir, manifest), /modified/);
});

test('Figma URLs and transport selection are explicit', () => {
  assert.equal(figmaTarget('https://www.figma.com/design/abc/Test?node-id=1-2').id, '1:2');
  for (const value of ['https://evilfigma.com/design/abc/?node-id=1-2', 'http://figma.com/design/abc/?node-id=1-2', 'https://figma.com/design/abc']) assert.throws(() => figmaTarget(value));
  assert.equal(figmaTransport(undefined, {}), 'browser');
  assert.equal(figmaTransport(undefined, { FIGMA_ACCESS_TOKEN: 'fixture' }), 'api');
  assert.throws(() => figmaTransport('api', {}), /FIGMA_ACCESS_TOKEN/);
});

test('Figma REST pins renders to one version and never sends tokens to the image host', async () => {
  const calls = [];
  const where = { key: 'example', id: '1:2' };
  const api = new FigmaAPI({ token: 'test-only', fetchImpl: async (url, options) => {
    calls.push({ url: new URL(url), options });
    if (new URL(url).hostname === 'images.example.test') return new Response('image');
    if (new URL(url).pathname.includes('/images/')) return Response.json({ images: { '1:2': 'https://images.example.test/frame.png' } });
    return Response.json({ name: 'Example', version: 'v42', document: { id: '0:0', type: 'DOCUMENT', children: [
      { id: '1:2', name: 'Frame', type: 'FRAME', absoluteBoundingBox: { x: 0, y: 0, width: 100, height: 200 } },
    ] } });
  } });
  const document = await api.document(where);
  const data = await api.export(where, document, [1]);
  assert.equal(data.screen.width, 100);
  assert.equal(calls[1].url.searchParams.get('version'), 'v42');
  assert.equal(calls[0].options.headers['X-Figma-Token'], 'test-only');
  assert.equal(calls[2].options.headers, undefined);
});

test('Figma rate limits are explicit and do not trigger a fallback or retry loop', async () => {
  let calls = 0;
  const api = new FigmaAPI({ token: 'test-only', fetchImpl: async () => { calls++; return new Response('', { status: 429, headers: { 'retry-after': '60' } }); } });
  await assert.rejects(api.document({ key: 'example', id: '1:2' }), /retry after 60/);
  assert.equal(calls, 1);
});

test('a removed solid background is a measured difference', () => {
  const result = compare({ frame: { id: 'root', width: 100, height: 100 }, nodes: [
    { id: 'root', ref: 'root', name: 'Panel', box: [0, 0, 100, 100], fills: [{ color: '#112233' }] },
  ] }, { root: { width: 100, height: 100 }, texts: [], boxes: [] });
  assert.ok(result.issues.some((issue) => issue.kind === 'colour' && issue.text.includes('transparent')));
});

test('an unhealthy existing stand is reported and never replaced', async (t) => {
  const dir = temp(t);
  const server = http.createServer((_request, response) => response.writeHead(503).end());
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(() => close(server));
  fs.mkdirSync(path.join(dir, '.git'));
  fs.writeFileSync(path.join(dir, '.agentgate.json'), JSON.stringify({ design: { stand: { command: ['does-not-exist'], ready: `http://127.0.0.1:${server.address().port}` } } }));
  fs.writeFileSync(path.join(dir, 'screen.json'), '{"url":"/"}');
  await assert.rejects(ensureStand(dir), /existing stand responds HTTP 503/);
  assert.equal(server.listening, true);
});

async function standFixture(t, { recorded = false, ignoreTerm = false, readyTimeout = 3 } = {}) {
  const dir = temp(t);
  const listener = http.createServer();
  await new Promise((resolve) => listener.listen(0, '127.0.0.1', resolve));
  const port = listener.address().port;
  await close(listener);
  const url = `http://127.0.0.1:${port}`;
  fs.mkdirSync(path.join(dir, '.git'));
  fs.writeFileSync(path.join(dir, '.agentgate.json'), JSON.stringify({ design: { stand: {
    command: [process.execPath, 'server.mjs'], ready: url, ready_timeout_s: readyTimeout, graphql_env: ['GRAPHQL_URL'],
  } } }));
  fs.writeFileSync(path.join(dir, 'screen.json'), '{"url":"/"}');
  fs.writeFileSync(path.join(dir, 'server.mjs'), `import fs from 'node:fs'; import http from 'node:http';
fs.writeFileSync('server.pid', String(process.pid));
const server=http.createServer(async (_request,response)=>{
  const result=process.env.GRAPHQL_URL ? await (await fetch(process.env.GRAPHQL_URL,{method:'POST',body:'{"operationName":"Example"}'})).json() : {ok:true};
  response.end(JSON.stringify(result));
});
server.listen(${port},'127.0.0.1');
process.on('SIGTERM',()=>{${ignoreTerm ? '' : 'server.close(()=>process.exit(0));'}});
`);
  if (recorded) {
    fs.mkdirSync(path.join(dir, 'fixtures'));
    fs.writeFileSync(path.join(dir, 'fixtures', 'Example.json'), '{"data":{"value":42}}');
  }
  return { dir, url };
}

test('owned stands are stopped, including a child that ignores SIGTERM', { timeout: 10000 }, async (t) => {
  const { dir, url } = await standFixture(t, { ignoreTerm: true });
  const stop = await ensureStand(dir);
  t.after(stop);
  assert.deepEqual(await (await fetch(url)).json(), { ok: true });
  await stop();
  await assert.rejects(fetch(url));
  await stop(); // repeated cleanup is safe
});

test('recorded fixture subprocess follows the owned stand lifecycle', { timeout: 10000 }, async (t) => {
  const { dir, url } = await standFixture(t, { recorded: true });
  const stop = await ensureStand(dir);
  t.after(stop);
  assert.deepEqual(await (await fetch(url)).json(), { data: { value: 42 } });
  await stop();
  await assert.rejects(fetch(url));
});

test('a nonexistent stand executable fails promptly and restores signal listeners', { timeout: 10000 }, async (t) => {
  const { dir } = await standFixture(t);
  const config = JSON.parse(fs.readFileSync(path.join(dir, '.agentgate.json')));
  config.design.stand.command = ['agentgate-nonexistent-test-executable'];
  fs.writeFileSync(path.join(dir, '.agentgate.json'), JSON.stringify(config));
  const before = process.listenerCount('SIGTERM');
  await assert.rejects(ensureStand(dir), /ENOENT/);
  assert.equal(process.listenerCount('SIGTERM'), before);
});
