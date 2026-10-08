// adr: 785
import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { AppsService, FaaSClient } from '../src/index.js';

test('original runtime consent and delivery requests never follow redirects', async (t) => {
  let calls = 0;
  let redirected = 0;
  const server = createServer((req, res) => {
    calls++;
    if (req.url === '/recipient') { redirected++; res.end('{}'); return; }
    req.resume();
    res.writeHead(307, { Location: '/recipient' });
    res.end();
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address();
  assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'directory-key', retry: { maxAttempts: 1, backoffMs: 0 } });
  const id = '7c93ae23-83ed-4dc9-987a-4676cd3ca6c2';
  const requests = [
    () => AppsService.putGlobalRuntimeConsent({ slug: 'global-shop', consentId: id, requestBody: { region: 'eu-fsn', expires_at: '2026-10-08T10:00:00Z', regional_api_key: 'request-only-key' } }),
    () => AppsService.revokeGlobalRuntimeConsent({ slug: 'global-shop', consentId: id, requestBody: { regional_api_key: 'request-only-key' } }),
    () => AppsService.deliverGlobalRuntimeQuota({ slug: 'global-shop', allocationId: id, region: 'eu-fsn', requestBody: { regional_api_key: 'request-only-key' } }),
    () => AppsService.queueGlobalQuotaDelivery({ slug: 'global-shop', allocationId: id, deliveryId: id, requestBody: { region: 'eu-fsn', purpose: 'grant', runtime_consent_id: id } }),
  ];
  for (const request of requests) {
    const before = calls;
    await assert.rejects(request());
    assert.equal(calls, before + 1);
  }
  assert.equal(redirected, 0);
});
