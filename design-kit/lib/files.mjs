import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';

export function writeAtomic(file, content, { mode = 0o600 } = {}) {
  const temporary = path.join(path.dirname(file), `.${path.basename(file)}.${crypto.randomUUID()}.tmp`);
  let fd;
  try {
    fd = fs.openSync(temporary, 'wx', mode);
    fs.writeFileSync(fd, content);
    fs.fsyncSync(fd);
    fs.closeSync(fd);
    fd = undefined;
    fs.renameSync(temporary, file);
  } finally {
    if (fd !== undefined) fs.closeSync(fd);
    fs.rmSync(temporary, { force: true });
  }
}

export function writeJSON(file, value) {
  writeAtomic(file, JSON.stringify(value, null, 2) + '\n');
}

// Publish complete exports only. A failed download never destroys the last usable package.
export async function replacePackage(destination, build) {
  const target = path.resolve(destination);
  fs.mkdirSync(path.dirname(target), { recursive: true });
  const lock = `${target}.export.lock`;
  let lockFD;
  let acquired = false;
  let staging;
  let backup;
  let moved = false;
  try {
    try { lockFD = fs.openSync(lock, 'wx', 0o600); acquired = true; }
    catch (error) {
      if (error.code === 'EEXIST') throw new Error(`another export owns ${target}; if its process stopped, remove ${lock} and retry`);
      throw error;
    }
    fs.writeFileSync(lockFD, `${process.pid}\n`);
    fs.closeSync(lockFD);
    lockFD = undefined;
    staging = fs.mkdtempSync(path.join(path.dirname(target), `.${path.basename(target)}.export-`));
    backup = `${staging}.previous`;
    if (fs.existsSync(target)) {
      const stat = fs.lstatSync(target);
      if (!stat.isDirectory() || stat.isSymbolicLink()) throw new Error(`${target} must be a directory, not a link`);
      if (fs.readdirSync(target).length && !fs.existsSync(path.join(target, 'manifest.json'))) throw new Error(`${target} is not an existing design package or an empty directory`);
    }
    const result = await build(staging);
    if (!fs.existsSync(path.join(staging, 'manifest.json'))) throw new Error('export did not produce a manifest');
    if (fs.existsSync(target)) {
      fs.renameSync(target, backup);
      moved = true;
    }
    try {
      fs.renameSync(staging, target);
    } catch (error) {
      if (moved) fs.renameSync(backup, target);
      moved = false;
      throw error;
    }
    if (moved) fs.rmSync(backup, { recursive: true, force: true });
    return result;
  } finally {
    if (lockFD !== undefined) { try { fs.closeSync(lockFD); } catch { /* preserve the original failure */ } }
    if (staging) fs.rmSync(staging, { recursive: true, force: true });
    if (acquired) fs.rmSync(lock, { force: true });
  }
}
