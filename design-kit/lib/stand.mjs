// Own only the processes started for this check. Existing stands are never stopped or reconfigured.
import { execFileSync, fork, spawn } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { httpURL, numberOption } from './cli.mjs';

const json = (file) => JSON.parse(fs.readFileSync(file, 'utf8'));
const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

export function repository(from) {
  for (const start of [path.resolve(from), process.cwd()]) {
    for (let dir = start; ; dir = path.dirname(dir)) {
      if (fs.existsSync(path.join(dir, '.git'))) return dir;
      if (dir === path.dirname(dir)) break;
    }
  }
  return process.cwd();
}

export function projectDesign(from) {
  const root = repository(from);
  const file = path.join(root, '.agentgate.json');
  const config = process.env.AGENTGATE_EXECUTABLE
    ? JSON.parse(execFileSync(process.env.AGENTGATE_EXECUTABLE, ['config', 'show'], { cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'], timeout: 10000 }))
    : fs.existsSync(file) ? json(file) : {};
  const design = config.design;
  if (!design?.stand) throw new Error(`no design.stand is configured for ${root}: declare command and ready in ${file}`);
  const stand = design.stand;
  if (!Array.isArray(stand.command) || !stand.command.length || !stand.command.every((part) => typeof part === 'string' && part.length)) {
    throw new Error('design.stand.command must be a nonempty array of arguments');
  }
  httpURL(stand.ready, 'design.stand.ready');
  if (stand.ready_timeout_s !== undefined) numberOption(stand.ready_timeout_s, 'ready_timeout_s', { min: 1, max: 3600, integer: true });
  return design;
}

export function readScreen(dir) {
  const file = path.join(dir, 'screen.json');
  if (!fs.existsSync(file)) return null;
  const stand = projectDesign(dir).stand;
  const screen = json(file);
  if (!screen || typeof screen.url !== 'string' || !screen.url.trim()) throw new Error(`${file} needs a URL or relative page path`);
  screen.cookies ??= stand.cookies;
  const url = httpURL(new URL(screen.url, stand.ready).href, 'screen URL').href;
  const host = new URL(url).hostname;
  return { stand, screen, url, package: path.join(dir, 'package'), fixtures: path.join(dir, 'fixtures'),
    map: fs.existsSync(path.join(dir, 'map.json')) ? path.join(dir, 'map.json') : undefined,
    storageState: screen.cookies ? { cookies: screen.cookies.map((cookie) => ({ domain: host, path: '/', expires: -1,
      httpOnly: false, secure: new URL(url).protocol === 'https:', sameSite: 'Lax', ...cookie })), origins: [] } : undefined };
}

function envFile(file) {
  const out = {};
  if (!file) return out;
  for (const line of fs.readFileSync(file, 'utf8').split('\n')) {
    const match = /^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$/.exec(line);
    if (match) out[match[1]] = match[2].trim().replace(/^(["'])(.*)\1$/, '$2');
  }
  return out;
}

// Read parentage and start time in one /proc snapshot. Do not read a PID from ps
// and later adopt whatever process happens to occupy it as an owned descendant.
function processInfo(pid) {
  try {
    const raw = fs.readFileSync(`/proc/${pid}/stat`, 'utf8');
    const end = raw.lastIndexOf(') ');
    if (end < 0) return null;
    const fields = raw.slice(end + 2).trim().split(/\s+/);
    if (fields.length < 20 || !/^\d+$/.test(fields[19])) return null;
    return { pid, parent: Number(fields[1]), identity: fields[19], state: fields[0] };
  } catch { return null; }
}

function descendants(pid) {
  const parents = new Map();
  try {
    for (const name of fs.readdirSync('/proc')) {
      if (!/^\d+$/.test(name)) continue;
      const child = processInfo(Number(name));
      if (!child) continue;
      if (!parents.has(child.parent)) parents.set(child.parent, []);
      parents.get(child.parent).push(child);
    }
  } catch { /* the process group still handles ordinary children */ }
  const collect = (parent) => (parents.get(parent) || []).flatMap((child) => [child, ...collect(child.pid)]);
  const root = processInfo(pid);
  return root ? [root, ...collect(pid)] : [];
}

function identity(pid) {
  return processInfo(pid)?.identity || null;
}

const ownership = new WeakMap();
function own(child) {
  if (child.pid) ownership.set(child, { pid: child.pid, identity: identity(child.pid) });
  return child;
}

function stillOwned(child) {
  const owner = ownership.get(child);
  return owner?.identity && child.pid === owner.pid && child.exitCode === null && child.signalCode === null && identity(owner.pid) === owner.identity;
}

function signal(pid, name) {
  try { process.kill(pid, name); } catch (error) { if (error.code !== 'ESRCH') throw error; }
}

export async function stopProcess(child) {
  // Ownership is established at spawn, never at cleanup. A stale ChildProcess
  // object cannot acquire and kill a new process that reused the old PID.
  if (!stillOwned(child)) return;
  const members = descendants(child.pid);
  const running = () => members.filter((member) => {
    const current = processInfo(member.pid);
    return member.identity && current?.identity === member.identity && current.state !== 'Z';
  });
  if (stillOwned(child)) signal(-child.pid, 'SIGTERM');
  for (const member of members.reverse()) if (member.identity && identity(member.pid) === member.identity) signal(member.pid, 'SIGTERM');
  for (const deadline = Date.now() + 1200; running().length && Date.now() < deadline;) await pause(40);
  // PID identities protect against signalling a new process after a fast PID reuse.
  for (const member of running()) signal(member.pid, 'SIGKILL');
  for (const deadline = Date.now() + 500; child.exitCode === null && child.signalCode === null && Date.now() < deadline;) await pause(20);
}

async function status(url) {
  try {
    const response = await fetch(url, { signal: AbortSignal.timeout(1000), redirect: 'manual' });
    await response.body?.cancel();
    return response.status;
  } catch { return null; }
}

async function fixtureProcess(dir, record, origin, children, logFD) {
  const args = [dir, '--port', '0', '--origin', origin];
  if (record) args.push('--record', record);
  const child = own(fork(fileURLToPath(new URL('../bin/graphql-fixtures.mjs', import.meta.url)), args,
    { detached: true, stdio: ['ignore', logFD, logFD, 'ipc'] }));
  children.push(child);
  const port = await new Promise((resolve, reject) => {
    const timer = setTimeout(() => done(new Error('fixture server did not become ready')), 5000);
    const exited = () => done(new Error('fixture server exited before becoming ready'));
    const errored = (error) => done(error);
    const message = (value) => { if (value?.type === 'ready') done(null, value.port); };
    function done(error, value) {
      clearTimeout(timer);
      child.removeListener('exit', exited); child.removeListener('error', errored); child.removeListener('message', message);
      if (error) reject(error); else resolve(value);
    }
    child.once('exit', exited); child.once('error', errored); child.on('message', message);
  });
  child.disconnect();
  return port;
}

export async function ensureStand(dir, { record, keep = false, log = () => {} } = {}) {
  const description = readScreen(dir);
  if (!description) throw new Error(`${dir} is not a screen (screen.json is missing)`);
  const { stand, fixtures } = description;
  const existing = await status(stand.ready);
  if (existing !== null) {
    if (record !== undefined) throw new Error('the stand already runs; stop it before --record so its backend can be configured');
    if (existing >= 400) throw new Error(`the existing stand responds HTTP ${existing} at ${stand.ready}; fix it or choose a healthy ready URL`);
    log(`using the existing stand at ${stand.ready}`);
    return async () => {};
  }
  const root = repository(dir);
  const env = { ...process.env, ...envFile(stand.env_file && path.resolve(root, stand.env_file)), ...(stand.env || {}) };
  if (record === true) record = env[(stand.graphql_env || [])[0]];
  if (!record && record !== undefined) throw new Error('--record found no backend: give its URL');
  if (record) httpURL(record, 'record gateway');
  const recorded = fs.existsSync(fixtures) && fs.readdirSync(fixtures).some((name) => name.endsWith('.json'));
  if ((recorded || record) && (!Array.isArray(stand.graphql_env) || !stand.graphql_env.length)) {
    throw new Error('recorded fixtures need design.stand.graphql_env to name the backend environment variable');
  }
  const logDir = fs.mkdtempSync(path.join(os.tmpdir(), 'agentgate-stand-'));
  const logFile = path.join(logDir, 'output.log');
  const fd = fs.openSync(logFile, 'wx', 0o600);
  const children = [];
  let stopping;
  const onExit = () => {
    for (const child of children) {
      if (stillOwned(child)) {
        try { signal(-child.pid, 'SIGKILL'); } catch { /* best effort during synchronous exit */ }
      }
    }
  };
  const onTerm = () => { void stop().finally(() => process.exit(143)); };
  const onInt = () => { void stop().finally(() => process.exit(130)); };
  const detachHandlers = () => {
    process.removeListener('exit', onExit); process.removeListener('SIGTERM', onTerm); process.removeListener('SIGINT', onInt);
  };
  const stop = () => {
    stopping ??= (async () => {
      try { await Promise.all(children.map(stopProcess)); } finally { detachHandlers(); fs.rmSync(logDir, { recursive: true, force: true }); }
    })();
    return stopping;
  };
  stop.release = () => {
    detachHandlers();
    children.forEach((child) => child.unref());
    log(`stand kept running (PIDs ${children.map((child) => child.pid).join(', ')}); log: ${logFile}`);
  };
  process.once('exit', onExit); process.once('SIGTERM', onTerm); process.once('SIGINT', onInt);
  try {
    if (recorded || record) {
      const port = await fixtureProcess(fixtures, record, new URL(stand.ready).origin, children, fd);
      for (const name of stand.graphql_env) env[name] = `http://127.0.0.1:${port}/graphql`;
    }
    log(`starting the stand: ${stand.command.join(' ')}`);
    const child = own(spawn(stand.command[0], stand.command.slice(1), { cwd: root, env, detached: true, stdio: ['ignore', fd, fd] }));
    children.push(child);
    let spawnError;
    child.once('error', (error) => { spawnError = error; });
    let lastStatus;
    for (const deadline = Date.now() + (stand.ready_timeout_s ?? 240) * 1000; Date.now() < deadline;) {
      if (spawnError) throw spawnError;
      if (child.exitCode !== null || child.signalCode !== null) break;
      lastStatus = await status(stand.ready);
      if (lastStatus !== null && lastStatus < 400) return stop;
      await pause(150);
    }
    if (spawnError) throw spawnError;
    const tail = fs.readFileSync(logFile, 'utf8').slice(-6000);
    throw new Error(`stand did not become ready at ${stand.ready}${lastStatus ? ` (HTTP ${lastStatus})` : ''}: ${stand.command.join(' ')}\n${tail}`);
  } catch (error) {
    await stop();
    throw error;
  } finally { fs.closeSync(fd); }
}
