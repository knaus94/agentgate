#!/usr/bin/env node
// check — does the page look as the design package says? Prints a short list of differences; exit 1 when there are any.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import { openBrowser } from '../lib/browser.mjs';
import { compare, defaults, summary } from '../lib/compare.mjs';
import { diffImages, readPage, still } from '../lib/page-read.mjs';
import { ensureStand, readScreen } from '../lib/stand.mjs';

const usage = `usage:
  layout new <screen> <figma-url> --url <path> --root <css> [--locale ru-RU]
                                                 make a screen: pull the design and write where it is shown
  layout <screen>                                compare a screen with its design: starts the stand when it is not running
        [--keep]                 leave the stand running afterwards
        [--record [<gateway>]]   show the screen through a real backend and keep its answers as the screen's fixtures
  layout <package> --url <page> [--root <css>]   compare any page with a design package written by \`figma pull\`
        --root <css>            the element that implements the pulled node (default: the whole document)
        [--storage-state <f>]   a browser session for the stand, as Playwright saves it
        [--wait <css>]          an element that must be there before anything is measured
        [--locale ru-RU]        the language the page is opened in
        [--viewport 1920x1080]  default: the size of the screen the node was drawn in
        [--device mobile]      mobile/touch Chromium (also for tablets); default desktop, even at narrow widths
        [--map <file>]          {"ignore": {"<handle>": "why"}, "dynamic": ["<handle>"], "select": {"<handle>": "tag.class"}}
        [--tolerance 2]         pixels a position or a size may be off by
        [--max-pixel-diff 0.1]  fail when more than this share of pixels differs (reported either way)
        [--out <dir>]           also write report.json, marked.png, diff.png and actual.png
        [--write-baseline <f>]  record the differences the page has now, and exit 0
        [--baseline <f>]        fail only for differences that are not in that record

exit: 0 measurements pass (visual acceptance is separate), 1 measured differences, 2 verification could not run`;

const fail = (message, code = 2) => {
  throw Object.assign(new Error(message), { exitCode: code });
};

async function main() {
  const flags = {};
  const rest = [];
  for (let index = 2; index < process.argv.length; index++) {
    const value = process.argv[index];
    if (value === '--keep') flags.keep = true;
    else if (value === '--record' && !/^https?:/.test(process.argv[index + 1] || '')) flags.record = true;
    else if (value.startsWith('--')) flags[value.slice(2)] = process.argv[++index];
    else rest.push(value);
  }
  if (process.argv.includes('--help') || process.argv.includes('-h') || rest[0] === 'help') {
    console.log(usage);
    return;
  }
  if (rest[0] === 'new') {
    // A screen is made in one go: the design and where it is shown.
    const [, target, figma] = rest;
    if (!target || !figma || !flags.url || !flags.root) fail(usage);
    fs.mkdirSync(target, { recursive: true });
    const { execFileSync } = await import('node:child_process');
    try {
      execFileSync(process.execPath, [new URL('./figma.mjs', import.meta.url).pathname, 'pull', figma, '--out', path.join(target, 'package')], { stdio: ['ignore', 'ignore', 'inherit'] });
    } catch {
      fail("could not import the design; see the exporter error above");
    }
    // A screen somebody described already keeps its description: only the design was missing.
    if (!fs.existsSync(path.join(target, 'screen.json'))) {
      fs.writeFileSync(path.join(target, 'screen.json'), JSON.stringify({ url: flags.url, root: flags.root, locale: flags.locale, viewport: flags.viewport, device: flags.device }, null, 2) + '\n');
    }
    const brief = fs.readFileSync(path.join(target, 'package', 'brief.md'), 'utf8');
    console.log(JSON.stringify({ screen: target, brief: path.join(target, 'package', 'brief.md'), brief_tokens_about: Math.round(Buffer.byteLength(brief) / 3.2) }, null, 2));
    return;
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
    flags.url = new URL(flags.url || screen.url, screen.url).href;
    flags.root ??= screen.screen.root;
    flags.locale ??= screen.screen.locale;
    flags.viewport ??= screen.screen.viewport;
    flags.device ??= screen.screen.device;
    flags.map ??= screen.map;
  }
  if (!flags.url) fail(usage);
  const device = flags.device || 'desktop';
  if (!['desktop', 'mobile'].includes(device)) fail('device must be desktop or mobile');

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
  for (const [handle, selector] of Object.entries(map.select || {})) {
    if (!spec.nodes.some((node) => node.ref === handle) || typeof selector !== 'string' || !selector.trim()) {
      fail(`map.select needs a known layer handle and a CSS selector: ${handle}`);
    }
  }
  const limits = { ...defaults };
  if (flags.tolerance) limits.position = limits.size = Number(flags.tolerance);
  const [width, height] = (flags.viewport || `${manifest.screen?.width || spec.frame.width}x${Math.min(manifest.screen?.height || 1080, 1200)}`).split('x').map(Number);
  const contract = crypto.createHash('sha256').update(JSON.stringify({ package: manifest.digest,
    url: flags.url, root: flags.root || '', viewport: [width, height], device, locale: flags.locale, map, limits })).digest('hex');

  let stopStand = async () => {};
  if (screen) {
    stopStand = await ensureStand(rest[0], { record: flags.record, keep: flags.keep, log: (text) => console.error(`layout: ${text}`) }).catch((error) => fail(error.message));
  }
  let browser;
  let code = 0;
  try {
    browser = await openBrowser();
    const context = await browser.newContext({ viewport: { width, height }, deviceScaleFactor: 1, reducedMotion: 'reduce',
      isMobile: device === 'mobile', hasTouch: device === 'mobile',
      locale: flags.locale, storageState: flags['storage-state'] || screen?.storageState });
    const page = await context.newPage();
    await page.goto(flags.url, { waitUntil: 'load', timeout: 60000 }).catch((error) => fail(`the page did not open: ${String(error).split('\n')[0]}`));
    await page.waitForLoadState('networkidle', { timeout: 15000 }).catch(() => {});
    if (flags.wait) await page.waitForSelector(flags.wait, { timeout: 30000 }).catch(() => fail(`${flags.wait} never appeared at ${page.url()}`));
    await page.addStyleTag({ content: still });
    await page.evaluate(() => document.fonts.ready);
    await page.waitForTimeout(400);

    const seen = await page.evaluate(readPage, { root: flags.root || '', select: map.select });
    if (seen.missingRoot && flags['write-baseline']) {
      // The page or its root is not there before the work: it is what the task builds.
      fs.writeFileSync(flags['write-baseline'], JSON.stringify({ contract, known: [] }) + '\n');
      console.log(`design baseline: ${page.url()} does not show “${spec.frame.name}” yet`);
      return;
    }
    if (seen.error) fail(`${seen.error} at ${page.url()}`);
    const measurement = { device, viewport: { width, height }, referenceScreen: manifest.screen, documentWidth: seen.documentWidth,
      horizontalOverflow: seen.horizontalOverflow, root: { selector: flags.root || 'html', ...seen.root } };
    const measurementLine = `measurement: ${device} ${width}x${height}; document width ${measurement.documentWidth}; root ${measurement.root.selector} ${seen.root.width}x${seen.root.height} at ${seen.root.x},${seen.root.y}; padding ${seen.root.padding.join('/')}; horizontal overflow ${measurement.horizontalOverflow}`;
    console.log(measurementLine);
    const result = compare(spec, seen, map, limits);
    if (result.contentCompared === 0 && flags['write-baseline']) {
      // Nothing is built yet: every difference the finished screen has will be a new one.
      fs.writeFileSync(flags['write-baseline'], JSON.stringify({ contract, known: [] }) + '\n');
      console.log(`design baseline: the page shows nothing of “${spec.frame.name}” yet`);
      return;
    }
    if (result.compared === 0) fail(`nothing on ${page.url()} corresponds to the design: is --root the element that implements “${spec.frame.name}”?`);
    // Only an unchanged discrepancy is grandfathered. Matching a kind alone would hide a larger offset or
    // a newly wrong font under an older finding of the same kind.
    const keyOf = (issue) => JSON.stringify([issue.ref, issue.kind, issue.text]);
    if (flags['write-baseline']) {
      // A page that shows next to nothing of the design is not a screen with differences: it is not built, and
      // nothing it lacks may count as known.
      const built = result.compared >= result.total / 10;
      fs.writeFileSync(flags['write-baseline'], JSON.stringify({ contract, known: built ? result.issues.map(keyOf) : [] }) + '\n');
      console.log(built ? `design baseline: ${result.issues.length} differences recorded` : `design baseline: the page shows ${result.compared} of ${result.total} layers — taken as not built yet`);
      return;
    }
    if (flags.baseline) {
      // No record of the page before the work means nothing about it is known: every difference counts.
      const baseline = fs.existsSync(flags.baseline) ? JSON.parse(fs.readFileSync(flags.baseline, 'utf8')) : null;
      const known = new Set(baseline?.contract === contract ? baseline.known : []);
      result.known = result.issues.filter((issue) => known.has(keyOf(issue))).length;
      result.issues = result.issues.filter((issue) => !known.has(keyOf(issue)));
    }

    const contextShot = await page.screenshot({ fullPage: true });
    const shot = flags.root ? await page.locator(flags.root).first().screenshot() : contextShot;
    const blank = await context.newPage();
    const data = (bytes) => `data:image/png;base64,${bytes.toString('base64')}`;
    const pictures = await blank.evaluate(diffImages, { design: data(fs.readFileSync(path.join(dir, 'frame.png'))), actual: data(shot),
      crop: { x: 0, y: 0, width: spec.frame.width, height: spec.frame.height },
      marks: result.issues.slice(0, 20).map((issue, index) => ({ number: index + 1, box: issue.box, color: issue.kind === 'missing' ? '#ff3b30' : '#ffcc00' })).filter((mark) => mark.box) });

    let written;
    if (flags.out) {
      fs.mkdirSync(flags.out, { recursive: true });
      const save = (name, url) => fs.writeFileSync(path.join(flags.out, name), Buffer.from(url.split(',')[1], 'base64'));
      save('marked.png', pictures.marked);
      save('diff.png', pictures.diff);
      fs.writeFileSync(path.join(flags.out, 'actual.png'), shot);
      fs.writeFileSync(path.join(flags.out, 'page.png'), contextShot);
      fs.writeFileSync(path.join(flags.out, 'report.json'), JSON.stringify({ url: page.url(), package: manifest.digest, measurement, pixels: pictures.ratio, ...result }, null, 1));
      written = { marked: path.join(flags.out, 'marked.png'), diff: path.join(flags.out, 'diff.png') };
    }
    const resultSummary = summary(result, pictures.ratio, written);
    const text = measurementLine + '\n' + resultSummary;
    if (written) fs.writeFileSync(path.join(flags.out, 'report.md'), text);
    process.stdout.write(resultSummary);
    const tooDifferent = flags['max-pixel-diff'] !== undefined && pictures.ratio > Number(flags['max-pixel-diff']);
    if (tooDifferent) console.log(`more than ${flags['max-pixel-diff']} of the pixels differ`);
    code = result.issues.length || tooDifferent ? 1 : 0;
  } finally {
    await browser?.close().catch(() => {});
    if (!flags.keep) await stopStand();
  }
  process.exitCode = code;
}

main().catch((error) => {
  console.error(`check: ${error.message || error}`);
  process.exitCode = error.exitCode || 2;
});
