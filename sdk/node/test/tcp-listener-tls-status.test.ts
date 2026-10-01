import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { AppsService } from '../src/generated/services/AppsService.js';
import { OpenAPI } from '../src/generated/core/OpenAPI.js';

test('TLS status transport preserves unknown evidence without expiry', async () => {
  const wire = { name: 'echo', tls: { mode: 'terminate', hostname: 'echo.example' }, enabled: true,
    scope: 'observed_edges', observations: [{ edge_id: 'edge-one', status: 'unknown', observed_at: '2026-10-01T12:00:00Z' }] };
  const captured: unknown[] = [];
  const server = createServer((request, response) => {
    captured.push({ method: request.method, path: request.url, authorization: request.headers.authorization });
    response.writeHead(200, { 'Content-Type': 'application/json' });
    response.end(JSON.stringify(wire));
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  const address = server.address();
  assert.ok(address && typeof address !== 'string');
  const previous = { base: OpenAPI.BASE, token: OpenAPI.TOKEN };
  OpenAPI.BASE = `http://127.0.0.1:${address.port}`;
  OpenAPI.TOKEN = 'test-key';
  try {
    const result = await AppsService.appTcpListenerTlsStatus({ slug: 'app', name: 'echo' });
    assert.deepEqual(result, wire);
    assert.ok(result.observations[0]);
    assert.equal(result.observations[0].not_after, undefined);
    assert.deepEqual(captured, [{ method: 'GET', path: '/v1/apps/app/tcp-listeners/echo/tls-status', authorization: 'Bearer test-key' }]);
  } finally {
    OpenAPI.BASE = previous.base;
    OpenAPI.TOKEN = previous.token;
    server.closeAllConnections();
    await new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
  }
});
