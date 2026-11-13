// A screen and the stand that shows it. A project says once how its front end starts and which variables point
// at its backend; a screen says where it is. Nobody writes a script: `check <screen>` starts what is not
// running, with the screen's recorded answers in place of the backend, and stops what it started.
import { execFileSync, spawn } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { serveFixtures } from './fixtures.mjs';

const json = (file) => JSON.parse(fs.readFileSync(file, 'utf8'));

// The project's file says how its front end starts; <screen>/screen.json says where the screen is.
export function projectDesign(from) {
  const file = path.join(repository(from), '.agentgate.json');
  const design = fs.existsSync(file) ? json(file).design : null;
  if (!design || !design.stand) {
    throw new Error(`${file} does not say how the front end starts: add "design": {"stand": {"command": […], "ready": "http://localhost:<port>"}}`);
  }
  return design;
}

export function readScreen(dir) {
  const screenFile = path.join(dir, 'screen.json');
  if (!fs.existsSync(screenFile)) return null;
  const stand = projectDesign(dir).stand;
  const screen = json(screenFile);
  screen.cookies ??= stand.cookies;
  const url = new URL(screen.url, stand.ready).href;
  const host = new URL(url).hostname;
  return { stand, screen, url, package: path.join(dir, 'package'), fixtures: path.join(dir, 'fixtures'),
    map: fs.existsSync(path.join(dir, 'map.json')) ? path.join(dir, 'map.json') : undefined,
    storageState: screen.cookies ? { cookies: screen.cookies.map((cookie) => ({ domain: host, path: '/', expires: -1, httpOnly: false, secure: false, sameSite: 'Lax', ...cookie })), origins: [] } : undefined };
}

function repository(from) {
  for (let dir = path.resolve(from); dir !== path.dirname(dir); dir = path.dirname(dir)) {
    if (fs.existsSync(path.join(dir, '.git'))) return dir;
  }
  for (let dir = process.cwd(); dir !== path.dirname(dir); dir = path.dirname(dir)) {
    if (fs.existsSync(path.join(dir, '.git'))) return dir;
  }
  return process.cwd();
}

function envFile(file) {
  const out = {};
  if (!file || !fs.existsSync(file)) return out;
  for (const line of fs.readFileSync(file, 'utf8').split('\n')) {
    const match = /^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$/.exec(line);
    if (match) out[match[1]] = match[2].trim().replace(/^(["'])(.*)\1$/, '$2');
  }
  return out;
}

// A dev server is started through a runner that puts it into a group of its own, so a signal to one group
// misses it: every descendant is found and told.
function stopTree(pid) {
  const all = [];
  const visit = (parent) => {
    all.push(parent);
    let children = '';
    try {
      children = execFileSync('ps', ['-o', 'pid=', '--ppid', String(parent)], { encoding: 'utf8' });
    } catch {
      // no children
    }
    for (const child of children.split('\n').map((line) => Number(line.trim())).filter(Boolean)) visit(child);
  };
  visit(pid);
  for (const one of all.reverse()) {
    try {
      process.kill(one, 'SIGTERM');
    } catch {
      // already gone
    }
  }
}

const answers = async (url) => {
  try {
    await fetch(url, { signal: AbortSignal.timeout(3000), redirect: 'manual' });
    return true;
  } catch {
    return false;
  }
};

// Returns how to stop what was started here; a stand that already runs is used and left alone.
export async function ensureStand(dir, { record, keep, log = () => {} } = {}) {
  const { stand, fixtures } = readScreen(dir);
  if (await answers(stand.ready)) return async () => {};
  const root = repository(dir);
  const env = { ...process.env, ...envFile(stand.env_file && path.join(root, stand.env_file)), ...(stand.env || {}) };
  // `--record` by itself records from the backend the project's own environment names.
  if (record === true) record = env[(stand.graphql_env || [])[0]];
  if (!record && record !== undefined) throw new Error('`--record` found no backend: give its URL');
  // Recorded answers stand in for the backend when there are any; a screen without them is shown by the
  // backend the project's own environment names.
  const recorded = fs.existsSync(fixtures) && fs.readdirSync(fixtures).some((name) => name.endsWith('.json'));
  const server = recorded || record ? await serveFixtures({ dir: fixtures, port: 0, record }) : null;
  if (server) for (const name of stand.graphql_env || []) env[name] = `http://127.0.0.1:${server.address().port}/graphql`;
  log(`starting the stand: ${stand.command.join(' ')}`);
  const child = spawn(stand.command[0], stand.command.slice(1), { cwd: root, env, detached: true, stdio: 'ignore' });
  let stopping;
  const onExit = () => { if (child.pid) stopTree(child.pid); };
  const onSignal = () => { void stop().finally(() => process.exit(143)); };
  const stop = async () => {
    if (stopping) return stopping;
    stopping = (async () => {
      onExit();
      if (server) await new Promise((resolve) => server.close(resolve));
      process.removeListener('exit', onExit);
      process.removeListener('SIGTERM', onSignal);
      process.removeListener('SIGINT', onSignal);
    })();
    return stopping;
  };
  // However this process ends, what it started ends with it.
  if (!keep) {
    process.once('exit', onExit);
    process.once('SIGTERM', onSignal);
    process.once('SIGINT', onSignal);
  }
  let exited = false;
  let spawnError;
  child.once('exit', () => { exited = true; });
  child.once('error', (error) => { spawnError = error; exited = true; });
  for (const deadline = Date.now() + (stand.ready_timeout_s || 240) * 1000; Date.now() < deadline;) {
    if (exited) break;
    if (await answers(stand.ready)) return stop;
    await new Promise((resolve) => setTimeout(resolve, 1500));
  }
  await stop();
  if (spawnError) throw spawnError;
  throw new Error(`the stand did not answer at ${stand.ready}: ${stand.command.join(' ')}`);
}
