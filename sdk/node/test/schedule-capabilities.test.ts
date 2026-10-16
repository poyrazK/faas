import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { AccountService, FaaSClient, type CapabilitiesResponse } from '../src/index.js';

for (const advertised of [false, true]) {
  test(`schedule capabilities preserve explicit support (advertised=${advertised})`, async (t) => {
    const body: CapabilitiesResponse = { registry_version: 1, plan: 'free', capabilities: [] };
    if (advertised) {
      body.environment_cache_schedules = { listing: true, activity: true, checkpoint: false, retention_seconds: 2592000 };
    }
    const server = createServer((req, res) => {
      assert.equal(req.method, 'GET');
      assert.equal(req.url, '/v1/capabilities');
      assert.equal(req.headers.authorization, 'Bearer fixture-token');
      res.setHeader('Content-Type', 'application/json');
      res.end(JSON.stringify(body));
    });
    server.listen(0, '127.0.0.1');
    await once(server, 'listening');
    t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
    const address = server.address();
    assert.ok(address && typeof address !== 'string');
    new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture-token', retry: { maxAttempts: 1, backoffMs: 0 } });
    const result = await AccountService.getCapabilities();
    assert.deepEqual(result, body);
    assert.equal(result.environment_cache_schedules?.checkpoint, advertised ? false : undefined);
  });
}
