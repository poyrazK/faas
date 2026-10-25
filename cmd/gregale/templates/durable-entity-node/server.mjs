import {createServer} from 'node:http';
import {fileURLToPath} from 'node:url';
import {readFile} from 'node:fs/promises';
import {DURABLE_ENTITY_HANDLER_PATH, DURABLE_ENTITY_RESTORE_VALIDATION_PATH, DURABLE_ENTITY_MAX_REQUEST_BYTES} from '@gregale/sdk-node/preview/durable-entities/runtime';
import {registry} from './registry.mjs';

export function readBody(request, maximum = DURABLE_ENTITY_MAX_REQUEST_BYTES) {
  return new Promise((resolve, reject) => {
    const chunks = []; let bytes = 0; let tooLarge = false;
    request.on('data', chunk => {
      bytes += chunk.length;
      if (bytes > maximum) {
        if (!tooLarge) { tooLarge = true; chunks.length = 0; reject(new RangeError('body too large')); }
      } else if (!tooLarge) chunks.push(chunk);
    });
    request.on('end', () => { if (!tooLarge) resolve(Buffer.concat(chunks)); });
    request.on('error', reject);
    request.on('aborted', () => reject(new Error('request aborted')));
  });
}

export function createEntityServer() {
  return createServer(async (request, response) => {
    response.setHeader('Cache-Control', 'no-store');
    if (request.method === 'GET' && request.url === '/healthz') { response.end('ok'); return; }
    if (request.method !== 'POST' || ![DURABLE_ENTITY_HANDLER_PATH, DURABLE_ENTITY_RESTORE_VALIDATION_PATH].includes(request.url)) { response.writeHead(404).end(); return; }
    try {
      const body = await readBody(request);
      const result = request.url === DURABLE_ENTITY_HANDLER_PATH ? registry.handle(body) : registry.validateRestore(body);
      response.writeHead(200, {'Content-Type': 'application/json'}).end(result);
    } catch (error) {
      response.writeHead(error instanceof RangeError ? 413 : 422).end('invalid entity request');
    }
  });
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const project = JSON.parse(await readFile(new URL('gregale.entities.json', import.meta.url), 'utf8'));
  const expected = project.handlers.map(({namespace, schema_version}) => ({namespace, schemaVersion: schema_version}));
  if (JSON.stringify(registry.describe()) !== JSON.stringify(expected)) throw new Error('entity registration differs from gregale.entities.json');
  createEntityServer().listen(Number(process.env.PORT ?? 8080), '0.0.0.0');
}
