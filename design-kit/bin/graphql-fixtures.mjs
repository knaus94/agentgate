#!/usr/bin/env node
// fixtures — a GraphQL endpoint that answers from files, so a screen can be shown with nothing behind it.
import { serveFixtures } from '../lib/fixtures.mjs';

const usage = `usage:
  fixtures <dir> [--port 5999]                  answer every operation from <dir>
  fixtures <dir> --record <gateway-url>         pass operations to the gateway and keep its answers

An answer is <dir>/<OperationName>.json. When the same operation is asked with different variables, the answer
for one set of them is <dir>/<OperationName>.<digest>.json; the plain file answers the rest.`;

const flags = {};
const rest = [];
for (let index = 2; index < process.argv.length; index++) {
  const value = process.argv[index];
  if (value.startsWith('--')) flags[value.slice(2)] = process.argv[++index];
  else rest.push(value);
}
if (!rest[0]) {
  console.error(usage);
  process.exit(2);
}
const port = Number(flags.port || 5999);
await serveFixtures({ dir: rest[0], port, record: flags.record });
console.error(`fixtures: ${flags.record ? `recording ${flags.record} into` : 'answering from'} ${rest[0]} on http://127.0.0.1:${port}/graphql`);
