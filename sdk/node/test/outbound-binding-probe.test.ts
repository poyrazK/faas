import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';

import { FaaSClient, OutboundService, type OutboundBindingProbePolicy } from '../src/index.js';

test('outbound probe policy SDK methods preserve routing, credentials and responses', async (t) => {
  const integration = '01234567-89ab-cdef-0123-456789abcdef';
  const path = `/v1/outbound/integrations/${integration}/probe-policy`;
  const policy: OutboundBindingProbePolicy = { method: 'HEAD', path: '/health', expected_status: 204 };
  const requests: Array<{ method: string | undefined; path: string | undefined; body: unknown }> = [];
  const server = createServer((req, res) => {
    void (async () => {
      let body = '';
      for await (const chunk of req) body += String(chunk);
      assert.equal(req.headers.authorization, 'Bearer fixture-token');
      requests.push({ method: req.method, path: req.url, body: body ? JSON.parse(body) : undefined });
      if (req.method === 'DELETE') { res.writeHead(204); res.end(); return; }
      res.setHeader('Content-Type', 'application/json');
      res.end(JSON.stringify(policy));
    })().catch(error => { res.statusCode = 500; res.end(JSON.stringify({ message: String(error) })); });
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address();
  assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture-token', retry: { maxAttempts: 1, backoffMs: 0 } });
  assert.deepEqual(await OutboundService.setOutboundBindingProbePolicy({ integration, requestBody: policy }), policy);
  assert.deepEqual(await OutboundService.getOutboundBindingProbePolicy({ integration }), policy);
  await OutboundService.deleteOutboundBindingProbePolicy({ integration });
  assert.deepEqual(requests, [
    { method: 'PUT', path, body: policy },
    { method: 'GET', path, body: undefined },
    { method: 'DELETE', path, body: undefined },
  ]);
});
