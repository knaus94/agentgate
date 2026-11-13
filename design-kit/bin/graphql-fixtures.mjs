#!/usr/bin/env node
// graphql-fixtures — a GraphQL endpoint that answers from files, so a screen can be shown in a chosen state
// with nothing behind it. Record once against the real gateway, edit the answers to the state the design
// shows, then serve them to the browser and to server-side rendering alike.
import crypto from 'node:crypto';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';

const usage = `usage:
  graphql-fixtures <dir> [--port 5999]                  answer every operation from <dir>
  graphql-fixtures <dir> --record <gateway-url>         pass operations to the gateway and keep its answers

An answer is <dir>/<OperationName>.json. When the same operation is asked with different variables, the answer
for one set of them is <dir>/<OperationName>.<digest>.json; the plain file answers the rest.`;

const flags = {};
const rest = [];
for (let index = 2; index < process.argv.length; index++) {
  const value = process.argv[index];
  if (value.startsWith('--')) flags[value.slice(2)] = process.argv[++index];
  else rest.push(value);
}
const dir = rest[0];
if (!dir) {
  console.error(usage);
  process.exit(2);
}
fs.mkdirSync(dir, { recursive: true });
const port = Number(flags.port || 5999);

const nameOf = (operation) => operation.operationName || /(?:query|mutation|subscription)\s+(\w+)/.exec(operation.query || '')?.[1] ||
  'anonymous-' + crypto.createHash('sha256').update(operation.query || '').digest('hex').slice(0, 8);
const digestOf = (operation) => crypto.createHash('sha256').update(JSON.stringify(operation.variables || {})).digest('hex').slice(0, 8);

async function answer(operation, headers) {
  const name = nameOf(operation);
  const exact = path.join(dir, `${name}.${digestOf(operation)}.json`);
  const plain = path.join(dir, `${name}.json`);
  if (flags.record) {
    const response = await fetch(flags.record, { method: 'POST', body: JSON.stringify(operation),
      headers: { 'content-type': 'application/json', ...(headers.authorization ? { authorization: headers.authorization } : {}), ...(headers.cookie ? { cookie: headers.cookie } : {}) } });
    const body = await response.json();
    fs.writeFileSync(fs.existsSync(plain) && fs.readFileSync(plain, 'utf8') !== JSON.stringify(body, null, 1) + '\n' ? exact : plain, JSON.stringify(body, null, 1) + '\n');
    return body;
  }
  for (const file of [exact, plain]) {
    if (fs.existsSync(file)) return JSON.parse(fs.readFileSync(file, 'utf8'));
  }
  console.error(`no answer for ${name}: add ${plain}`);
  return { errors: [{ message: `no fixture for ${name}` }] };
}

http.createServer((request, response) => {
  // The browser asks from the page's origin with its cookies: that has to be allowed by name, not by a wildcard.
  response.setHeader('access-control-allow-origin', request.headers.origin || '*');
  response.setHeader('access-control-allow-credentials', 'true');
  response.setHeader('access-control-allow-headers', request.headers['access-control-request-headers'] || 'content-type');
  response.setHeader('access-control-allow-methods', 'POST, OPTIONS');
  if (request.method !== 'POST') {
    response.writeHead(request.method === 'OPTIONS' ? 204 : 405).end();
    return;
  }
  let raw = '';
  request.on('data', (chunk) => { raw += chunk; });
  request.on('end', async () => {
    try {
      const body = JSON.parse(raw);
      const out = Array.isArray(body) ? await Promise.all(body.map((operation) => answer(operation, request.headers))) : await answer(body, request.headers);
      response.writeHead(200, { 'content-type': 'application/json' }).end(JSON.stringify(out));
    } catch (error) {
      response.writeHead(200, { 'content-type': 'application/json' }).end(JSON.stringify({ errors: [{ message: String(error.message || error) }] }));
    }
  });
}).listen(port, '127.0.0.1', () => console.error(`graphql-fixtures: ${flags.record ? `recording ${flags.record} into` : 'answering from'} ${dir} on http://127.0.0.1:${port}/graphql`));
