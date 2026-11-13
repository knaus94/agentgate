#!/usr/bin/env node
// figma — a Figma session owned by this tool, and design packages pulled through it.
import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { openProfile, readSession, writeSession, figmaHome } from '../lib/browser.mjs';
import { probe, exportNode, exportAssets, listFrames, readVariables } from '../lib/figma-page.mjs';
import { assetName, brief, buildSpec, detail } from '../lib/outline.mjs';

const usage = `usage:
  figma login                         sign in to Figma in a browser window, once; the session stays in this tool's profile
  figma status                        who is signed in, and whether the session still works
  figma frames <figma-url>            the frames of the page the URL points into: choose what to pull
  figma pull <figma-url> --out <dir>  write the design package of the node the URL points at
        [--node <id>]                 another node of the same file instead of the one in the URL
        [--scales 2]                  extra PNG scales of the node, for tools
        [--no-assets]                 do not export icons and pictures separately
  figma node <dir> <handle> [--depth 3]   everything about one layer of a package and its inside
  figma import-cookies <file>         for a machine without a display: seed the profile from a browser cookie export

The profile lives in ${figmaHome} (AGENTGATE_FIGMA_HOME overrides it).`;

const fail = (message, code = 1) => {
  console.error(`figma: ${message}`);
  process.exit(code);
};

function parse(argv) {
  const flags = {};
  const rest = [];
  for (let index = 0; index < argv.length; index++) {
    const value = argv[index];
    if (!value.startsWith('--')) rest.push(value);
    else if (value.startsWith('--no-')) flags[value.slice(5)] = false;
    else flags[value.slice(2)] = argv[++index];
  }
  return { flags, rest };
}

// Figma keeps the signed-in address in a cookie of its own; nothing else is read from the profile.
async function signedIn(context) {
  const cookies = await context.cookies('https://www.figma.com');
  const authn = cookies.find((cookie) => cookie.name === '__Host-figma.authn');
  if (!authn) return null;
  let email;
  try {
    const prefs = cookies.find((cookie) => cookie.name === '__Host-figma.user_prefs');
    email = JSON.parse(Buffer.from(decodeURIComponent(prefs.value).split('.')[2], 'base64').toString('utf8'))[0].e;
  } catch {
    // the address is a courtesy; the session does not depend on it
  }
  return { email, expires: authn.expires > 0 ? new Date(authn.expires * 1000).toISOString() : undefined };
}

async function verify(context) {
  const page = context.pages()[0] || await context.newPage();
  await page.goto('https://www.figma.com/files', { waitUntil: 'domcontentloaded', timeout: 60000 });
  await page.waitForTimeout(2500);
  return /\/login|\/signup/.test(new URL(page.url()).pathname) ? null : signedIn(context);
}

async function login() {
  if (!process.env.DISPLAY && !process.env.WAYLAND_DISPLAY) {
    fail('signing in needs a display: run `figma login` on a desktop, or seed the profile with `figma import-cookies`');
  }
  const { context, channel } = await openProfile({ headless: false, channel: readSession()?.channel });
  const page = context.pages()[0] || await context.newPage();
  await page.goto('https://www.figma.com/login', { waitUntil: 'domcontentloaded' }).catch(() => {});
  console.error('A browser window is open. Sign in to Figma there; this command finishes by itself.');
  const deadline = Date.now() + 10 * 60 * 1000;
  let who = null;
  while (Date.now() < deadline && !who) {
    await new Promise((resolve) => setTimeout(resolve, 1500));
    if (context.pages().length === 0) break;
    const current = context.pages()[context.pages().length - 1];
    if (!/\/login|\/signup|accounts\.google|\/oauth/.test(current.url())) who = await signedIn(context).catch(() => null);
  }
  await context.close().catch(() => {});
  if (!who) fail('nobody signed in');
  writeSession({ channel, email: who.email, signedInAt: new Date().toISOString() });
  console.log(JSON.stringify({ signed_in: who.email || true, expires: who.expires }, null, 2));
}

async function status() {
  const session = readSession();
  if (!session) fail('no Figma session: run `figma login`');
  const { context } = await openProfile({ headless: true, channel: session.channel });
  const who = await verify(context).finally(() => context.close());
  if (!who) fail('the Figma session has expired: run `figma login`');
  console.log(JSON.stringify({ signed_in: who.email || true, expires: who.expires, browser: session.channel }, null, 2));
}

async function importCookies(file) {
  if (!file) fail(usage, 2);
  const sameSite = { lax: 'Lax', strict: 'Strict', no_restriction: 'None' };
  const cookies = JSON.parse(fs.readFileSync(file, 'utf8')).filter((cookie) => /(^|\.)figma\.com$/.test(cookie.domain)).map((cookie) => {
    const out = { name: cookie.name, value: cookie.value, httpOnly: !!cookie.httpOnly, secure: !!cookie.secure };
    if (cookie.expirationDate) out.expires = Math.floor(cookie.expirationDate);
    if (sameSite[cookie.sameSite]) out.sameSite = sameSite[cookie.sameSite];
    if (cookie.hostOnly) out.url = `https://${cookie.domain}/`;
    else Object.assign(out, { domain: cookie.domain, path: cookie.path || '/' });
    return out;
  });
  const { context, channel } = await openProfile({ headless: true, channel: readSession()?.channel });
  await context.addCookies(cookies);
  const who = await verify(context).finally(() => context.close());
  if (!who) fail('these cookies do not hold a working Figma session');
  writeSession({ channel, email: who.email, signedInAt: new Date().toISOString() });
  console.log(JSON.stringify({ signed_in: who.email || true, expires: who.expires, next: `delete ${file}: the session now lives in the profile` }, null, 2));
}

function target(url, node) {
  let parsed;
  try {
    parsed = new URL(url);
  } catch {
    fail(`not a URL: ${url}`, 2);
  }
  const key = /^\/(?:design|file|proto|board)\/([A-Za-z0-9]+)/.exec(parsed.pathname)?.[1];
  const id = (node || parsed.searchParams.get('node-id') || '').replace(/^(\d+)-(\d+)$/, '$1:$2');
  if (!/(^|\.)figma\.com$/.test(parsed.hostname) || !key || !/^\d+:\d+$/.test(id)) {
    fail('this takes a Figma file URL with a node: copy the link to the frame ("Copy link to selection")', 2);
  }
  return { key, id, url: `https://www.figma.com/design/${key}/?node-id=${id.replace(':', '-')}` };
}

// Opens the file at a node and waits until the application can answer for it.
async function withNode(url, node, work) {
  const session = readSession();
  if (!session) fail('no Figma session: run `figma login`');
  const where = target(url, node);
  const { context } = await openProfile({ headless: true, channel: session.channel });
  try {
    const page = context.pages()[0] || await context.newPage();
    await page.goto(where.url, { waitUntil: 'domcontentloaded', timeout: 60000 });
    let state = { reason: 'the page did not load' };
    for (const deadline = Date.now() + 120000; Date.now() < deadline;) {
      state = await page.evaluate(probe, where.id).catch((error) => ({ ready: false, reason: String(error).split('\n')[0] }));
      if (state.ready || state.fatal) break;
      await page.waitForTimeout(1500);
    }
    if (!state.ready) fail(state.reason);
    return await work(page, where, state);
  } finally {
    await context.close().catch(() => {});
  }
}

async function frames(url) {
  if (!url) fail(usage, 2);
  await withNode(url, null, async (page, where) => {
    const listed = await page.evaluate(listFrames, where.id);
    console.log(`page “${listed.page}” — pull one with: figma pull <url> --node <id> --out <dir>`);
    for (const row of listed.rows) console.log(`${'  '.repeat(row.level)}${row.id}  ${row.name}  ${row.width}×${row.height}${row.type === 'frame' ? '' : '  ' + row.type}`);
  });
}

const sha256 = (data) => crypto.createHash('sha256').update(data).digest('hex');

async function pull(url, flags) {
  if (!url || !flags.out) fail(usage, 2);
  const out = path.resolve(flags.out);
  if (fs.existsSync(out) && fs.readdirSync(out).length > 0 && !fs.existsSync(path.join(out, 'manifest.json'))) {
    fail(`${out} holds something else: a package is written to an empty directory or over an earlier package`, 2);
  }
  const scales = [...new Set([1, ...String(flags.scales || '').split(',').map(Number).filter((scale) => scale > 1 && scale <= 4)])];
  await withNode(url, flags.node, async (page, where, state) => {
    const data = await page.evaluate(exportNode, { nodeId: where.id, scales, svg: false });
    const raw = JSON.stringify(data.rest);
    const variables = await page.evaluate(readVariables, [...new Set(raw.match(/VariableID:\d+:\d+/g) || [])]);
    const spec = buildSpec(data.rest, variables);

    for (const stale of ['assets', 'data']) fs.rmSync(path.join(out, stale), { recursive: true, force: true });
    fs.mkdirSync(path.join(out, 'assets'), { recursive: true });
    fs.mkdirSync(path.join(out, 'data'), { recursive: true });
    const files = {};
    const write = (name, content) => {
      fs.writeFileSync(path.join(out, name), content);
      files[name] = sha256(content);
    };
    if (flags.assets !== false) {
      const wanted = spec.nodes.filter((node) => node.asset).slice(0, 300);
      const written = new Map();
      for (let index = 0; index < wanted.length; index += 25) {
        const batch = wanted.slice(index, index + 25);
        const rendered = await page.evaluate(exportAssets, { rootId: where.id, requests: batch.map((node) => ({ id: node.id, format: node.asset.format })) });
        for (const node of batch) {
          if (!rendered[node.id]) continue;
          const bytes = Buffer.from(rendered[node.id], 'base64');
          const digest = sha256(bytes);
          // The same icon used ten times is one file.
          if (!written.has(digest)) {
            written.set(digest, `assets/${assetName(node)}`);
            write(written.get(digest), bytes);
          }
          node.asset.file = written.get(digest);
        }
      }
    }
    for (const scale of scales) write(scale === 1 ? 'frame.png' : `data/frame@${scale}x.png`, Buffer.from(data.png[scale], 'base64'));
    write('data/nodes.json', raw);
    write('data/variables.json', JSON.stringify(variables));
    write('data/spec.json', JSON.stringify(spec));
    const text = brief(spec, `Figma “${state.file}”, node ${where.id}, file version ${state.version}`, flags.out);
    write('brief.md', text);
    const manifest = { tool: 'agentgate-design 0.1', source: { url: where.url, file: state.file, key: where.key, node: where.id, version: state.version },
      frame: spec.frame, screen: data.screen, pulled_at: new Date().toISOString(),
      // What the package holds, apart from when it was pulled: a contract pins this.
      digest: sha256(Object.keys(files).sort().map((name) => `${name}:${files[name]}`).join('\n')), files };
    fs.writeFileSync(path.join(out, 'manifest.json'), JSON.stringify(manifest, null, 2) + '\n');
    console.log(JSON.stringify({ package: flags.out, frame: spec.frame, layers: spec.nodes.length,
      assets: Object.keys(files).filter((name) => name.startsWith('assets/')).length,
      // What a worker will carry in its context for reading the brief.
      brief: { file: path.join(flags.out, 'brief.md'), lines: text.split('\n').length, tokens_about: Math.round(Buffer.byteLength(text) / 3.2) },
      digest: manifest.digest }, null, 2));
  });
}

function node(dir, handle, flags) {
  if (!dir || !handle) fail(usage, 2);
  let spec;
  try {
    spec = JSON.parse(fs.readFileSync(path.join(dir, 'data', 'spec.json'), 'utf8'));
  } catch {
    fail(`${dir} is not a design package`, 2);
  }
  const text = detail(spec, handle, flags.depth === undefined ? 3 : Number(flags.depth));
  if (!text) fail(`no layer ${handle} in ${dir}`);
  process.stdout.write(text);
}

const [command, ...argv] = process.argv.slice(2);
const { flags, rest } = parse(argv);
const commands = { login, status, frames: () => frames(rest[0]), pull: () => pull(rest[0], flags),
  node: async () => node(rest[0], rest[1], flags), 'import-cookies': () => importCookies(rest[0]) };
if (!commands[command]) fail(usage, 2);
commands[command]().catch((error) => fail(String(error.message || error).split('\n')[0]));
