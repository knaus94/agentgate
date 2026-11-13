#!/usr/bin/env node
// check — does the page look as the design package says? Prints a short list of differences; exit 1 when there are any.
import fs from 'node:fs';
import path from 'node:path';
import { openBrowser } from '../lib/browser.mjs';
import { compare, defaults, summary } from '../lib/compare.mjs';
import { diffImages, readPage, still } from '../lib/page-read.mjs';
import { ensureStand, readScreen } from '../lib/stand.mjs';

const usage = `usage:
  layout <screen>                                compare a screen with its design: starts the stand when it is not running
        [--keep]                 leave the stand running afterwards
        [--record <gateway>]     start the stand against a real gateway and keep its answers as the screen's fixtures
  layout <package> --url <page> [--root <css>]   compare any page with a design package written by \`figma pull\`
        --root <css>            the element that implements the pulled node (default: the whole document)
        [--storage-state <f>]   a browser session for the stand, as Playwright saves it
        [--wait <css>]          an element that must be there before anything is measured
        [--locale ru-RU]        the language the page is opened in
        [--viewport 1920x1080]  default: the size of the screen the node was drawn in
        [--map <file>]          {"ignore": {"<handle>": "why"}, "dynamic": ["<handle>"], "select": {"<handle>": "tag.class"}}
        [--tolerance 2]         pixels a position or a size may be off by
        [--max-pixel-diff 0.1]  fail when more than this share of pixels differs (reported either way)
        [--out <dir>]           also write report.json, marked.png, diff.png and actual.png
        [--write-baseline <f>]  record the differences the page has now, and exit 0
        [--baseline <f>]        fail only for differences that are not in that record

exit: 0 the page matches (or is no worse than the baseline), 1 it differs, 2 nothing could be compared`;

const fail = (message, code = 2) => {
  console.error(`check: ${message}`);
  process.exit(code);
};

const flags = {};
const rest = [];
for (let index = 2; index < process.argv.length; index++) {
  const value = process.argv[index];
  if (value === '--keep') flags.keep = true;
  else if (value.startsWith('--')) flags[value.slice(2)] = process.argv[++index];
  else rest.push(value);
}
let dir = rest[0];
if (!dir) fail(usage);
// A screen is a directory that knows its own page: nothing else has to be said.
let screen = null;
try {
  screen = readScreen(dir);
} catch (error) {
  fail(error.message);
}
if (screen) {
  dir = screen.package;
  flags.url ??= screen.url;
  flags.root ??= screen.screen.root;
  flags.locale ??= screen.screen.locale;
  flags.viewport ??= screen.screen.viewport;
  flags.map ??= screen.map;
}
if (!flags.url) fail(usage);

const read = (name) => {
  try {
    return JSON.parse(fs.readFileSync(path.join(dir, name), 'utf8'));
  } catch {
    return fail(`${dir} is not a design package: ${name} is missing`);
  }
};
const manifest = read('manifest.json');
const spec = read(path.join('data', 'spec.json'));
const map = flags.map ? JSON.parse(fs.readFileSync(flags.map, 'utf8')) : {};
for (const [handle, why] of Object.entries(map.ignore || {})) {
  if (!String(why).trim()) fail(`the map ignores ${handle} without saying why`);
}
const limits = { ...defaults };
if (flags.tolerance) limits.position = limits.size = Number(flags.tolerance);
const [width, height] = (flags.viewport || `${manifest.screen?.width || spec.frame.width}x${Math.min(manifest.screen?.height || 1080, 1200)}`).split('x').map(Number);

let stopStand = async () => {};
if (screen) {
  stopStand = await ensureStand(rest[0], { record: flags.record, keep: flags.keep, log: (text) => console.error(`layout: ${text}`) }).catch((error) => fail(error.message));
}
const browser = await openBrowser();
let code = 0;
try {
  const context = await browser.newContext({ viewport: { width, height }, deviceScaleFactor: 1, reducedMotion: 'reduce',
    locale: flags.locale, storageState: flags['storage-state'] || screen?.storageState });
  const page = await context.newPage();
  await page.goto(flags.url, { waitUntil: 'load', timeout: 60000 }).catch((error) => fail(`the page did not open: ${String(error).split('\n')[0]}`));
  await page.waitForLoadState('networkidle', { timeout: 15000 }).catch(() => {});
  if (flags.wait) await page.waitForSelector(flags.wait, { timeout: 30000 }).catch(() => fail(`${flags.wait} never appeared at ${page.url()}`));
  await page.addStyleTag({ content: still });
  await page.evaluate(() => document.fonts.ready);
  await page.waitForTimeout(400);

  const seen = await page.evaluate(readPage, flags.root || '');
  if (seen.error) fail(`${seen.error} at ${page.url()}`);
  const result = compare(spec, seen, map, limits);
  if (result.compared === 0 && flags['write-baseline']) {
    // Nothing is built yet: every difference the finished screen has will be a new one.
    fs.writeFileSync(flags['write-baseline'], JSON.stringify({ known: [] }) + '\n');
    console.log(`design baseline: the page shows nothing of “${spec.frame.name}” yet`);
    process.exit(0);
  }
  if (result.compared === 0) fail(`nothing on ${page.url()} corresponds to the design: is --root the element that implements “${spec.frame.name}”?`);
  // A difference is known by the layer and what kind it is; how large it is may still change.
  const keyOf = (issue) => `${issue.ref}|${issue.kind}`;
  if (flags['write-baseline']) {
    fs.writeFileSync(flags['write-baseline'], JSON.stringify({ known: result.issues.map(keyOf) }) + '\n');
    console.log(`design baseline: ${result.issues.length} differences recorded`);
    process.exit(0);
  }
  if (flags.baseline) {
    const known = new Set(JSON.parse(fs.readFileSync(flags.baseline, 'utf8')).known);
    result.known = result.issues.filter((issue) => known.has(keyOf(issue))).length;
    result.issues = result.issues.filter((issue) => !known.has(keyOf(issue)));
  }

  const shot = flags.root ? await page.locator(flags.root).first().screenshot() : await page.screenshot({ fullPage: true });
  const blank = await context.newPage();
  const data = (bytes) => `data:image/png;base64,${bytes.toString('base64')}`;
  const pictures = await blank.evaluate(diffImages, { design: data(fs.readFileSync(path.join(dir, 'frame.png'))), actual: data(shot),
    crop: { x: 0, y: 0, width: spec.frame.width, height: spec.frame.height },
    marks: result.issues.slice(0, 20).map((issue, index) => ({ number: index + 1, box: issue.box, color: issue.kind === 'missing' ? '#ff3b30' : '#ffcc00' })).filter((mark) => mark.box) });

  let written;
  if (flags.out) {
    // A worker runs the same command in a sandbox where this directory is read-only: the pictures are the gate's.
    try {
      fs.mkdirSync(flags.out, { recursive: true });
      const save = (name, url) => fs.writeFileSync(path.join(flags.out, name), Buffer.from(url.split(',')[1], 'base64'));
      save('marked.png', pictures.marked);
      save('diff.png', pictures.diff);
      fs.writeFileSync(path.join(flags.out, 'actual.png'), shot);
      fs.writeFileSync(path.join(flags.out, 'report.json'), JSON.stringify({ url: page.url(), package: manifest.digest, pixels: pictures.ratio, ...result }, null, 1));
      written = { marked: path.join(flags.out, 'marked.png'), diff: path.join(flags.out, 'diff.png') };
    } catch {
      written = undefined;
    }
  }
  const text = summary(result, pictures.ratio, written);
  if (written) fs.writeFileSync(path.join(flags.out, 'report.md'), text);
  process.stdout.write(text);
  const tooDifferent = flags['max-pixel-diff'] !== undefined && pictures.ratio > Number(flags['max-pixel-diff']);
  if (tooDifferent) console.log(`more than ${flags['max-pixel-diff']} of the pixels differ`);
  code = result.issues.length || tooDifferent ? 1 : 0;
} finally {
  await browser.close().catch(() => {});
  if (!flags.keep) await stopStand();
}
process.exit(code);
