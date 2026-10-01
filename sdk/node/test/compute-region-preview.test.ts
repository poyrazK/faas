import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { DeploydiffService, OpenAPI } from '../src/index.js';

test('regional preview transports immutable placement through the public SDK', async () => {
  const captured: unknown[] = [];
  const server = createServer(async (request, response) => {
    const chunks: Buffer[] = [];
    for await (const chunk of request) chunks.push(Buffer.from(chunk));
    captured.push({ method: request.method, path: request.url, body: JSON.parse(Buffer.concat(chunks).toString()) });
    response.writeHead(200, { 'Content-Type': 'application/json' });
    response.end(JSON.stringify({ slug: 'regional', blocking: false,
      diff: { slug: 'regional', changes: [], breaks: [], compute_region: 'eu-fsn' } }));
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  const address = server.address();
  assert.ok(address && typeof address !== 'string');
  const previous = { base: OpenAPI.BASE, token: OpenAPI.TOKEN };
  OpenAPI.BASE = `http://127.0.0.1:${address.port}`;
  OpenAPI.TOKEN = 'test-key';
  try {
    const preview = await DeploydiffService.diffApp({ slug: 'regional', requestBody: { compute_region: 'eu-fsn' } });
    assert.equal(preview.diff.compute_region, 'eu-fsn');
    assert.equal(preview.blocking, false);
    assert.deepEqual(captured, [{ method: 'POST', path: '/v1/apps/regional/diff', body: { compute_region: 'eu-fsn' } }]);
  } finally {
    OpenAPI.BASE = previous.base;
    OpenAPI.TOKEN = previous.token;
    server.closeAllConnections();
    await new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
  }
});
