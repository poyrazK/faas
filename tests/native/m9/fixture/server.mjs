import { randomUUID } from 'node:crypto';
import { createServer } from 'node:http';
import { pathToFileURL } from 'node:url';

const canonicalID = (value) => typeof value === 'string' &&
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value) &&
  value !== '00000000-0000-0000-0000-000000000000';

// Deliberately in memory: a cold-boot fallback loses both the boot identity
// and operation receipts, and therefore cannot pass the migration drill.
export function fixtureServer(fixtureID) {
  if (!canonicalID(fixtureID)) throw new Error('GREGALE_M9_FIXTURE_ID is required');
  const bootID = randomUUID();
  const operations = new Map();
  return createServer(async (request, response) => {
    if (request.method === 'GET' && request.url === '/healthz') {
      response.writeHead(200).end('ready');
      return;
    }
    if (request.method !== 'POST' || request.url !== '/probe') {
      response.writeHead(404).end();
      return;
    }
    try {
      let body = '';
      for await (const chunk of request) {
        body += chunk;
        if (body.length > 1024) throw new Error('probe too large');
      }
      const input = JSON.parse(body);
      if (input.fixture_id !== fixtureID || !canonicalID(input.operation_id)) {
        response.writeHead(400).end();
        return;
      }
      if (!operations.has(input.operation_id)) {
        if (operations.size >= 128) {
          response.writeHead(429).end();
          return;
        }
        operations.set(input.operation_id, operations.size + 1);
      }
      response.writeHead(200, {'content-type': 'application/json'});
      response.end(JSON.stringify({
        fixture_id: fixtureID, boot_id: bootID, operation_id: input.operation_id,
        sequence: operations.get(input.operation_id), unique_operations: operations.size,
      }));
    } catch {
      response.writeHead(400).end();
    }
  });
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  fixtureServer(process.env.GREGALE_M9_FIXTURE_ID).listen(Number(process.env.PORT ?? 8080), '0.0.0.0');
}
