import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';

import { AppsService, FaaSClient } from '../src/index.js';

test('generated container listener methods preserve HTTP wire contracts', async (t) => {
  const requests: Array<{ method: string | undefined; path: string | undefined; body: unknown; auth: string | undefined }> = [];
  const server = createServer((req, res) => {
    void (async () => {
      let body = '';
      for await (const chunk of req) body += String(chunk);
      requests.push({ method: req.method, path: req.url, body: body ? JSON.parse(body) : undefined, auth: req.headers.authorization });
      res.setHeader('Content-Type', 'application/json');
      if (req.url?.endsWith('/tls-status')) {
        res.end(JSON.stringify({ name: 'echo', tls: { mode: 'terminate', hostname: 'echo.example' },
          enabled: true, scope: 'observed_edges', observations: [
            { edge_id: 'edge-one', status: 'unknown', observed_at: '2026-10-01T12:00:00Z' },
          ] }));
      } else if (req.url?.endsWith('/udp-listeners')) {
        res.end(JSON.stringify({ id: 'listener', name: 'dns', guest_port: 5353, public_port: 40150,
          protocol: 'udp', enabled: false, created_at: '2026-10-01T12:00:00Z', updated_at: '2026-10-01T12:00:00Z' }));
      } else {
        res.end('{}');
      }
    })().catch(() => { res.statusCode = 500; res.end('{}'); });
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address();
  assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture-token', retry: { maxAttempts: 1, backoffMs: 0 } });
  await AppsService.updateAppTcpListener({ slug: 'app', name: 'echo', requestBody: { enabled: true } });
  await AppsService.updateAppTcpListener({ slug: 'app', name: 'echo', requestBody: { tls: { mode: 'terminate', hostname: 'echo.example' } } });
  const udp = await AppsService.createAppUdpListener({ slug: 'app', requestBody: { name: 'dns', guest_port: 5353 } });
  assert.equal(udp.enabled, false);
  const status = await AppsService.appTcpListenerTlsStatus({ slug: 'app', name: 'echo' });
  assert.equal(status.scope, 'observed_edges');
  assert.equal(status.observations[0]?.status, 'unknown');
  assert.equal(status.observations[0]?.not_after, undefined);
  assert.deepEqual(requests.map(({ method, path, body }) => ({ method, path, body })), [
    { method: 'PATCH', path: '/v1/apps/app/tcp-listeners/echo', body: { enabled: true } },
    { method: 'PATCH', path: '/v1/apps/app/tcp-listeners/echo', body: { tls: { mode: 'terminate', hostname: 'echo.example' } } },
    { method: 'POST', path: '/v1/apps/app/udp-listeners', body: { name: 'dns', guest_port: 5353 } },
    { method: 'GET', path: '/v1/apps/app/tcp-listeners/echo/tls-status', body: undefined },
  ]);
  assert.ok(requests.every(request => request.auth === 'Bearer fixture-token'));
});
