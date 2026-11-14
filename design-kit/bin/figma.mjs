#!/usr/bin/env node
// figma — a Figma session owned by this tool, and design packages pulled through it.
import fs from 'node:fs';
import path from 'node:path';
import { openProfile, readSession, writeSession, figmaHome } from '../lib/browser.mjs';
import { probe, exportNode, exportAssets, listFrames, readVariables } from '../lib/figma-page.mjs';
import { detail } from '../lib/outline.mjs';
import { parseArgs, wantsHelp, numberOption } from '../lib/cli.mjs';
import { figmaTarget as target, figmaTransport } from '../lib/figma-target.mjs';
import { FigmaAPI } from '../lib/figma-api.mjs';
import { exportPackage, verifyPackage } from '../lib/package.mjs';

const usage = `usage:
  figma login                         sign in to Figma in a browser window, once; the session stays in this tool's profile
  figma status                        who is signed in, and whether the session still works
  figma frames <figma-url> [--json] [--depth 3]  a bounded catalogue for the preparation worker
  figma pull <figma-url> --out <dir>  write the design package of the node the URL points at
        [--node <id>]                 another node of the same file instead of the one in the URL
        [--scales 2]                  extra PNG scales of the node, for tools
        [--no-assets]                 do not export icons and pictures separately
        [--transport api|browser]     documented REST API or browser session (default: api when FIGMA_ACCESS_TOKEN is set)
  figma node <dir> <handle> [--depth 3]   everything about one layer of a package and its inside
  figma import-cookies <file>         for a machine without a display: seed the profile from a browser cookie export

REST uses FIGMA_ACCESS_TOKEN with file_content:read; tokens are never saved in a package.
Browser export uses Figma's internal scene graph and may change; no fallback occurs after an API error.
The browser profile lives in ${figmaHome} (AGENTGATE_FIGMA_HOME overrides it).`;

const fail = (message, code = 1) => {
  throw Object.assign(new Error(message), { exitCode: code });
};

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
  let who = null;
  try {
    const page = context.pages()[0] || await context.newPage();
    await page.goto('https://www.figma.com/login', { waitUntil: 'domcontentloaded', timeout: 60000 });
    console.error('A browser window is open. Sign in to Figma there; this command finishes by itself.');
    const deadline = Date.now() + 10 * 60 * 1000;
    while (Date.now() < deadline && !who) {
      await new Promise((resolve) => setTimeout(resolve, 1500));
      if (context.pages().length === 0) break;
      const current = context.pages()[context.pages().length - 1];
      if (!/\/login|\/signup|accounts\.google|\/oauth/.test(current.url())) who = await signedIn(context).catch(() => null);
    }
  } finally {
    await context.close().catch(() => {});
  }
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
  let who;
  try {
    await context.addCookies(cookies);
    who = await verify(context);
  } finally {
    await context.close().catch(() => {});
  }
  if (!who) fail('these cookies do not hold a working Figma session');
  writeSession({ channel, email: who.email, signedInAt: new Date().toISOString() });
  console.log(JSON.stringify({ signed_in: who.email || true, expires: who.expires, next: `delete ${file}: the session now lives in the profile` }, null, 2));
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

async function frames(url, flags) {
  if (!url) fail(usage, 2);
  const depth = numberOption(flags.depth ?? 1, 'depth', { min: 0, max: 6, integer: true });
  const transport = figmaTransport(flags.transport);
  const where = target(url, flags.node);
  const print = (listed) => {
    if (flags.json) console.log(JSON.stringify({ source: where.url, transport, ...listed }));
    else {
      console.log(`page “${listed.page}” — pull one with: figma pull <url> --node <id> --out <dir>`);
      for (const row of listed.rows) console.log(`${'  '.repeat(row.level)}${row.id}  ${row.name}  ${row.width}×${row.height}  ${row.type}`);
      if (listed.truncated) console.log('Catalogue truncated; select a narrower node.');
    }
  };
  if (transport === 'api') {
    const api = new FigmaAPI();
    print(api.catalogue(await api.document(where), depth));
  } else await withNode(url, flags.node, async (page) => print(await page.evaluate(listFrames, { nodeId: where.id, depth })));
}

async function pull(url, flags) {
  if (!url || !flags.out) fail(usage, 2);
  const transport = figmaTransport(flags.transport);
  const where = target(url, flags.node);
  const scales = [...new Set([1, ...(flags.scales === undefined ? [] : flags.scales.split(',').map((value) => numberOption(value, 'scale', { min: 1, max: 4 })))])];
  let result;
  if (transport === 'api') {
    const api = new FigmaAPI();
    const document = await api.document(where);
    const data = await api.export(where, document, scales);
    result = await exportPackage({ destination: flags.out, where, state: document.state, data, scales,
      transport, includeAssets: !flags['no-assets'],
      renderAssets: (requests) => api.assets(where, requests, document.file.version) });
  } else {
    result = await withNode(url, flags.node, async (page, selected, state) => {
      const data = await page.evaluate(exportNode, { nodeId: selected.id, scales, svg: false });
      if (!data.rest.document.absoluteBoundingBox) {
        const listed = await page.evaluate(listFrames, { nodeId: selected.id, depth: 2 });
        fail(`node ${selected.id} is a ${String(data.rest.document.type).toLowerCase()}, not a frame. Choose a child with --node:\n` +
          listed.rows.slice(0, 12).map((row) => `  ${row.id}  ${row.name}  ${row.width}×${row.height}`).join('\n'), 2);
      }
      const raw = JSON.stringify(data.rest);
      const variables = await page.evaluate(readVariables, [...new Set(raw.match(/VariableID:\d+:\d+/g) || [])]);
      return exportPackage({ destination: flags.out, where: selected, state, data, scales, variables,
        transport, includeAssets: !flags['no-assets'],
        renderAssets: (requests) => page.evaluate(exportAssets, { rootId: selected.id, requests }) });
    });
  }
  console.log(JSON.stringify(result, null, 2));
}

function node(dir, handle, flags) {
  if (!dir || !handle) fail(usage, 2);
  let spec;
  try {
    spec = JSON.parse(fs.readFileSync(path.join(dir, 'data', 'spec.json'), 'utf8'));
  } catch {
    fail(`${dir} is not a design package`, 2);
  }
  const manifest = JSON.parse(fs.readFileSync(path.join(dir, 'manifest.json'), 'utf8'));
  verifyPackage(dir, manifest);
  const text = detail(spec, handle, numberOption(flags.depth ?? 3, 'depth', { min: 0, max: 20, integer: true }));
  if (!text) fail(`no layer ${handle} in ${dir}`);
  process.stdout.write(text);
}

async function main() {
  const [command, ...argv] = process.argv.slice(2);
  if (!command || command === 'help' || wantsHelp([command, ...argv])) { console.log(usage); return; }
  const grammars = {
    login: {}, status: {}, 'import-cookies': {},
    frames: { booleans: ['json'], values: ['depth', 'node', 'transport'] },
    pull: { booleans: ['no-assets'], values: ['out', 'node', 'scales', 'transport'] },
    node: { values: ['depth'] },
  };
  if (!Object.hasOwn(grammars, command)) fail(`unknown command: ${command}\n${usage}`, 2);
  const { flags, rest } = parseArgs(argv, grammars[command]);
  const counts = { login: 0, status: 0, 'import-cookies': 1, frames: 1, pull: 1, node: 2 };
  if (rest.length !== counts[command]) fail(`${command} expects ${counts[command]} positional arguments\n${usage}`, 2);
  const commands = { login, status, frames: () => frames(rest[0], flags), pull: () => pull(rest[0], flags),
    node: () => node(rest[0], rest[1], flags), 'import-cookies': () => importCookies(rest[0]) };
  await commands[command]();
}

main().catch((error) => {
  console.error(`figma: ${error.message || error}`);
  process.exitCode = error.exitCode || 1;
});
