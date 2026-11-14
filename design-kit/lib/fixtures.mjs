// Local GraphQL fixtures. Missing recordings are visible failures; no network fallback occurs in replay mode.
import crypto from 'node:crypto';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { httpURL, numberOption } from './cli.mjs';
import { writeJSON } from './files.mjs';

const canonical = (value) => {
  if (Array.isArray(value)) return value.map(canonical);
  if (value && typeof value === 'object') return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonical(value[key])]));
  return value;
};
const hash = (value) => crypto.createHash('sha256').update(value).digest('hex').slice(0, 8);
const plainObject = (value) => value !== null && typeof value === 'object' && !Array.isArray(value);
const failure = (message, code) => ({ errors: [{ message, extensions: { code } }] });

export function fixtureIdentity(operation) {
  if (!plainObject(operation)) throw new Error('each GraphQL operation must be an object');
  if (operation.query !== undefined && typeof operation.query !== 'string') throw new Error('query must be a string');
  if (operation.operationName !== undefined && operation.operationName !== null &&
      (typeof operation.operationName !== 'string' || !/^[_A-Za-z][_0-9A-Za-z]{0,127}$/.test(operation.operationName))) {
    throw new Error('operationName must be a GraphQL name');
  }
  if (operation.variables != null && !plainObject(operation.variables)) throw new Error('variables must be an object');
  const name = operation.operationName || /(?:query|mutation|subscription)\s+([_A-Za-z][_0-9A-Za-z]*)/.exec(operation.query || '')?.[1] ||
    `anonymous-${hash(operation.query || JSON.stringify(canonical(operation.extensions || {})))}`;
  return { name, digest: hash(JSON.stringify(canonical(operation.variables || {}))),
    legacyDigest: hash(JSON.stringify(operation.variables || {})) };
}

function fixtureFile(dir, name) {
  const file = path.join(dir, name);
  try {
    if (!fs.lstatSync(file).isFile()) throw new Error(`fixture ${name} must be a regular file, not a link`);
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
  }
  return file;
}

export async function serveFixtures({ dir, port = 5999, record, allowMissing = false, maxBodyBytes = 1024 * 1024,
  recordTimeoutMs = 30000, fetchImpl = fetch, origins = [] }) {
  numberOption(port, 'port', { min: 0, max: 65535, integer: true });
  if (!dir || typeof dir !== 'string') throw new Error('a fixture directory is required');
  const root = path.resolve(dir);
  const upstream = record ? httpURL(record, 'record gateway').href : null;
  if (upstream) fs.mkdirSync(root, { recursive: true, mode: 0o700 });
  const answer = async (operation, headers) => {
    const { name, digest, legacyDigest } = fixtureIdentity(operation);
    const exact = fixtureFile(root, `${name}.${digest}.json`);
    const plain = fixtureFile(root, `${name}.json`);
    if (upstream) {
      const response = await fetchImpl(upstream, { method: 'POST', body: JSON.stringify(operation),
        redirect: 'error', signal: AbortSignal.timeout(recordTimeoutMs),
        headers: { 'content-type': 'application/json', ...(headers.authorization ? { authorization: headers.authorization } : {}),
          ...(headers.cookie ? { cookie: headers.cookie } : {}) } });
      if (!response.ok) throw new Error(`record gateway returned HTTP ${response.status}; no fixture was written`);
      const body = await response.json();
      if (!plainObject(body) || (!Object.hasOwn(body, 'data') && !Array.isArray(body.errors))) throw new Error('record gateway did not return a GraphQL response');
      writeJSON(exact, body);
      // A recording proves only this variables set. A plain fallback is an
      // explicit, manually authored fixture; inventing one from the first
      // response would silently replay another user's data for an unseen ID.
      return body;
    }
    for (const file of [...new Set([exact, fixtureFile(root, `${name}.${legacyDigest}.json`), plain])]) {
      if (fs.existsSync(file)) return JSON.parse(fs.readFileSync(file, 'utf8'));
    }
    return allowMissing ? { data: {} } : failure(`No fixture for ${name} (${digest})`, 'FIXTURE_NOT_FOUND');
  };
  const server = http.createServer((request, response) => {
    const send = (status, value) => {
      if (!response.writableEnded) response.writeHead(status, { 'content-type': 'application/json' }).end(JSON.stringify(value));
    };
    const origin = request.headers.origin;
    if (origin) {
      let local = false;
      try { local = ['127.0.0.1', 'localhost', '[::1]'].includes(new URL(origin).hostname); } catch { /* rejected below */ }
      if (!local && !origins.includes(origin)) {
        send(403, failure('origin is not a local stand or explicitly allowed', 'ORIGIN_DENIED'));
        request.resume();
        return;
      }
      response.setHeader('access-control-allow-origin', origin);
      response.setHeader('access-control-allow-credentials', 'true');
      response.setHeader('vary', 'Origin');
    }
    response.setHeader('access-control-allow-headers', 'content-type, authorization, apollo-require-preflight, x-apollo-operation-name');
    response.setHeader('access-control-allow-methods', 'POST, OPTIONS');
    if (!['/', '/graphql'].includes((request.url || '').split('?')[0])) {
      send(404, failure('use /graphql', 'NOT_FOUND'));
      request.resume();
      return;
    }
    if (request.method !== 'POST') {
      response.writeHead(request.method === 'OPTIONS' ? 204 : 405, { allow: 'POST, OPTIONS' }).end();
      request.resume();
      return;
    }
    const chunks = [];
    let bytes = 0;
    request.on('data', (chunk) => {
      bytes += chunk.length;
      if (bytes > maxBodyBytes) {
        chunks.length = 0;
        send(413, failure('request body is too large', 'BODY_TOO_LARGE'));
      } else if (!response.writableEnded) chunks.push(chunk);
    });
    request.on('error', () => send(400, failure('request body was interrupted', 'BAD_REQUEST')));
    request.on('end', async () => {
      if (response.writableEnded) return;
      let body;
      try {
        body = JSON.parse(Buffer.concat(chunks).toString('utf8'));
        const operations = Array.isArray(body) ? body : [body];
        if (!operations.length || operations.length > 100) throw new Error('batch must contain 1 to 100 operations');
        operations.forEach(fixtureIdentity);
      } catch (error) {
        send(400, failure(error.message, 'BAD_REQUEST'));
        return;
      }
      try {
        const out = Array.isArray(body) ? await Promise.all(body.map((operation) => answer(operation, request.headers))) : await answer(body, request.headers);
        send(200, out);
      } catch (error) {
        send(upstream ? 502 : 500, failure(error.message, upstream ? 'RECORD_FAILED' : 'FIXTURE_FAILED'));
      }
    });
  });
  server.requestTimeout = 35000;
  server.headersTimeout = 10000;
  server.keepAliveTimeout = 1000;
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(port, '127.0.0.1', () => { server.removeListener('error', reject); resolve(); });
  });
  return server;
}
