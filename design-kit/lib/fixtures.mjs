// A GraphQL endpoint that answers from files. Record once against the real gateway, edit the answers to the
// state the design shows, then serve them to the browser and to server-side rendering alike.
import crypto from 'node:crypto';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';

const nameOf = (operation) => operation.operationName || /(?:query|mutation|subscription)\s+(\w+)/.exec(operation.query || '')?.[1] ||
  'anonymous-' + crypto.createHash('sha256').update(operation.query || '').digest('hex').slice(0, 8);
const digestOf = (operation) => crypto.createHash('sha256').update(JSON.stringify(operation.variables || {})).digest('hex').slice(0, 8);

export function serveFixtures({ dir, port, record }) {
  if (record) fs.mkdirSync(dir, { recursive: true });
  const answer = async (operation, headers) => {
    const name = nameOf(operation);
    const exact = path.join(dir, `${name}.${digestOf(operation)}.json`);
    const plain = path.join(dir, `${name}.json`);
    if (record) {
      const response = await fetch(record, { method: 'POST', body: JSON.stringify(operation),
        headers: { 'content-type': 'application/json', ...(headers.authorization ? { authorization: headers.authorization } : {}), ...(headers.cookie ? { cookie: headers.cookie } : {}) } });
      const body = await response.json();
      const text = JSON.stringify(body, null, 1) + '\n';
      fs.writeFileSync(fs.existsSync(plain) && fs.readFileSync(plain, 'utf8') !== text ? exact : plain, text);
      return body;
    }
    for (const file of [exact, plain]) {
      if (fs.existsSync(file)) return JSON.parse(fs.readFileSync(file, 'utf8'));
    }
    // A screen asks for more than it shows; what nobody recorded is answered with nothing, not with an error on the page.
    return { data: {} };
  };
  const server = http.createServer((request, response) => {
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
  });
  return new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(port, '127.0.0.1', () => resolve(server));
  });
}
