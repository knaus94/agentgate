// Browser plumbing shared by the commands: where Playwright comes from, where the Figma profile lives and how
// a browser is started on it.
import { createRequire } from 'node:module';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

const require = createRequire(import.meta.url);

// The Figma session is a browser profile owned by this tool. Nobody exports or passes cookies: the user signs
// in once in a window, and every later run reuses the profile. agentgate hides `private/` from its workers.
export const figmaHome = process.env.AGENTGATE_FIGMA_HOME || path.join(os.homedir(), '.local', 'share', 'agentgate', 'private', 'figma');
export const profileDir = path.join(figmaHome, 'profile');
const sessionFile = path.join(figmaHome, 'session.json');

export function playwright() {
  const candidates = [process.env.PLAYWRIGHT_CORE, 'playwright-core',
    path.join(os.homedir(), '.local', 'share', 'agentgate', 'playwright-mcp', 'node_modules', 'playwright-core')];
  for (const candidate of candidates.filter(Boolean)) {
    try {
      return require(candidate);
    } catch {
      // try the next place
    }
  }
  throw new Error('playwright-core was not found: install the agentgate browser tooling or set PLAYWRIGHT_CORE');
}

export function readSession() {
  try {
    return JSON.parse(fs.readFileSync(sessionFile, 'utf8'));
  } catch {
    return null;
  }
}

export function writeSession(session) {
  fs.mkdirSync(figmaHome, { recursive: true, mode: 0o700 });
  fs.writeFileSync(sessionFile, JSON.stringify(session, null, 2) + '\n', { mode: 0o600 });
}

// Figma draws on WebGL; a headless browser has no GPU, so it renders in software.
const softwareGL = ['--use-angle=swiftshader', '--enable-unsafe-swiftshader', '--ignore-gpu-blocklist'];

// A profile is opened by the browser that created it: another build may refuse it or rewrite it.
export async function openProfile({ headless, channel }) {
  fs.mkdirSync(profileDir, { recursive: true, mode: 0o700 });
  const { chromium } = playwright();
  const options = {
    headless,
    viewport: headless ? { width: 1680, height: 1050 } : null,
    locale: 'en-US',
    // The cookie store must not depend on a desktop keyring: the same profile is read with and without a display.
    args: ['--password-store=basic', '--disable-blink-features=AutomationControlled', ...(headless ? softwareGL : [])],
    ignoreDefaultArgs: ['--enable-automation'],
  };
  const order = channel ? [channel] : ['chrome', 'chromium'];
  let failure;
  // One browser holds the profile at a time. Another command of this tool may be using it: that is a wait,
  // not a failure.
  for (const deadline = Date.now() + 120000; ;) {
    for (const name of order) {
      try {
        const context = await chromium.launchPersistentContext(profileDir, { ...options, channel: name });
        return { context, channel: name };
      } catch (error) {
        failure = error;
      }
    }
    if (!/ProcessSingleton|profile is already in use|SingletonLock/i.test(String(failure)) || Date.now() > deadline) break;
    await new Promise((resolve) => setTimeout(resolve, 2000));
  }
  throw new Error(`no browser could open the Figma profile: ${String(failure).split('\n')[0]}`);
}

// Plain headless browser for pages that are not Figma: the implementation under check.
export async function openBrowser() {
  const { chromium } = playwright();
  return chromium.launch({ headless: true, channel: 'chromium' });
}
