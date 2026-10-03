import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { AppsService } from '../src/generated/services/AppsService.js';
import { OpenAPI } from '../src/generated/core/OpenAPI.js';

test('generated TLS listener intent survives HTTP transport', async () => {
  const captured: unknown[] = [];
  const server = createServer(async (request, response) => {
    const chunks: Buffer[] = [];
    for await (const chunk of request) chunks.push(Buffer.from(chunk));
    const body = JSON.parse(Buffer.concat(chunks).toString()) as { tls: unknown };
    captured.push({ method: request.method, path: request.url, authorization: request.headers.authorization, body });
    response.writeHead(request.method === 'POST' ? 201 : 200, { 'Content-Type': 'application/json' });
    response.end(JSON.stringify({ id: 'listener', name: 'echo', guest_port: 9000, public_port: 40142,
      protocol: 'tcp', enabled: false, created_at: '2026-10-01T00:00:00Z', updated_at: '2026-10-01T00:00:00Z', tls: body.tls }));
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  const address = server.address();
  assert.ok(address && typeof address !== 'string');
  const previous = { base: OpenAPI.BASE, token: OpenAPI.TOKEN };
  OpenAPI.BASE = `http://127.0.0.1:${address.port}`;
  OpenAPI.TOKEN = 'test-key';
  try {
    const policy = { mode: 'terminate' as const, hostname: 'echo.example' };
    const created = await AppsService.createAppTcpListener({ slug: 'app', requestBody: { name: 'echo', guest_port: 9000, tls: policy } });
    assert.deepEqual(created.tls, policy);
    const updated = await AppsService.updateAppTcpListener({ slug: 'app', name: 'echo', requestBody: { tls: { mode: 'passthrough' } } });
    assert.equal(updated.tls.mode, 'passthrough');
    assert.deepEqual(captured, [
      { method: 'POST', path: '/v1/apps/app/tcp-listeners', authorization: 'Bearer test-key', body: { name: 'echo', guest_port: 9000, tls: policy } },
      { method: 'PATCH', path: '/v1/apps/app/tcp-listeners/echo', authorization: 'Bearer test-key', body: { tls: { mode: 'passthrough' } } },
    ]);
  } finally {
    OpenAPI.BASE = previous.base;
    OpenAPI.TOKEN = previous.token;
    server.closeAllConnections();
    await new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
  }
});
