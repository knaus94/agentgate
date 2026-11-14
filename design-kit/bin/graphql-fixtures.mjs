#!/usr/bin/env node
import { serveFixtures } from '../lib/fixtures.mjs';
import { numberOption, parseArgs, usageError, wantsHelp } from '../lib/cli.mjs';

const usage = `usage:
  fixtures <dir> [--port 5999] [--allow-missing]   serve recorded GraphQL responses locally
  fixtures <dir> --record <gateway-url>           record responses with a bounded upstream timeout

An answer is <OperationName>.<variables-digest>.json; <OperationName>.json is an optional fallback.
Missing recordings return FIXTURE_NOT_FOUND. --allow-missing explicitly returns empty data instead.
Only loopback origins are accepted by default; --origin <url> allows one named stand.
The server binds to 127.0.0.1. Ctrl-C stops it. --help never starts it.`;

async function main() {
  const argv = process.argv.slice(2);
  if (wantsHelp(argv) || argv[0] === 'help') { console.log(usage); return; }
  const { flags, rest } = parseArgs(argv, { booleans: ['allow-missing'], values: ['port', 'record', 'origin'] });
  if (rest.length !== 1) throw usageError(usage);
  const port = flags.port === undefined ? 5999 : numberOption(flags.port, 'port', { min: 0, max: 65535, integer: true });
  const server = await serveFixtures({ dir: rest[0], port, record: flags.record, allowMissing: !!flags['allow-missing'], origins: flags.origin ? [flags.origin] : [] });
  console.error(`fixtures: ${flags.record ? 'recording into' : 'answering from'} ${rest[0]} on http://127.0.0.1:${server.address().port}/graphql`);
  process.send?.({ type: 'ready', port: server.address().port });
  let stopping = false;
  const stop = () => {
    if (stopping) return;
    stopping = true;
    server.close();
    server.closeAllConnections();
  };
  process.once('SIGINT', stop);
  process.once('SIGTERM', stop);
}

main().catch((error) => { console.error(`fixtures: ${error.message}`); process.exitCode = error.exitCode || 1; });
