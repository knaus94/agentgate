// Shared argument grammar for the standalone design commands and the Go launcher.
export function wantsHelp(argv) {
  const end = argv.indexOf('--');
  return (end < 0 ? argv : argv.slice(0, end)).some((arg) => arg === '--help' || arg === '-h');
}

export function usageError(message) {
  return Object.assign(new Error(message), { exitCode: 2 });
}

export function parseArgs(argv, { booleans = [], values = [], optional = {} } = {}) {
  const flags = Object.create(null);
  const rest = [];
  const bool = new Set(booleans);
  const value = new Set(values);
  for (let index = 0; index < argv.length; index++) {
    const arg = argv[index];
    if (arg === '--') {
      rest.push(...argv.slice(index + 1));
      break;
    }
    if (!arg.startsWith('-') || arg === '-') {
      rest.push(arg);
      continue;
    }
    if (!arg.startsWith('--')) throw usageError(`unknown option: ${arg}`);
    const equal = arg.indexOf('=');
    const name = arg.slice(2, equal < 0 ? undefined : equal);
    const inline = equal < 0 ? undefined : arg.slice(equal + 1);
    if (!bool.has(name) && !value.has(name) && !Object.hasOwn(optional, name)) throw usageError(`unknown option: --${name}`);
    if (Object.hasOwn(flags, name)) throw usageError(`duplicate option: --${name}`);
    if (bool.has(name)) {
      if (inline !== undefined) throw usageError(`--${name} does not take a value`);
      flags[name] = true;
    } else if (Object.hasOwn(optional, name)) {
      flags[name] = inline ?? (optional[name](argv[index + 1] || '') ? argv[++index] : true);
      if (flags[name] === '') throw usageError(`--${name} needs a nonempty value`);
    } else {
      const next = inline ?? argv[++index];
      if (next === undefined || next === '' || next.startsWith('--')) throw usageError(`--${name} needs a value`);
      flags[name] = next;
    }
  }
  return { flags, rest };
}

export function numberOption(value, name, { min = 0, max = Number.MAX_SAFE_INTEGER, integer = false } = {}) {
  const parsed = Number(value);
  if (String(value).trim() === '' || !Number.isFinite(parsed) || parsed < min || parsed > max || (integer && !Number.isInteger(parsed))) {
    throw usageError(`${name} must be ${integer ? 'an integer' : 'a number'} from ${min} to ${max}`);
  }
  return parsed;
}

export function httpURL(value, name = 'URL') {
  let url;
  try { url = new URL(value); } catch { throw usageError(`${name} must be an absolute http:// or https:// URL`); }
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password) throw usageError(`${name} must be an HTTP URL without embedded credentials`);
  return url;
}

export function pageURL(value) {
  let url;
  try { url = new URL(value); } catch { throw usageError('page URL must be an absolute HTTP(S) or file URL'); }
  if (url.protocol === 'file:' && (!url.hostname || url.hostname === 'localhost')) return url;
  return httpURL(value, 'page URL');
}
