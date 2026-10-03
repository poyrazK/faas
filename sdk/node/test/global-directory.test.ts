import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { AppsService, DeploymentsService, deployCellAppMultipart, FaaSClient } from '../src/index.js';

test('regional ownership credentials never follow a directory redirect', async (t) => {
  let redirected = 0;
  let body = '';
  const server = createServer((req, res) => {
    void (async () => {
      if (req.url === '/recipient') { redirected++; res.end('{}'); return; }
      for await (const chunk of req) body += String(chunk);
      assert.equal(req.headers.authorization, 'Bearer directory-key');
      res.writeHead(307, { Location: '/recipient' });
      res.end();
    })().catch((error) => { res.destroy(error); });
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address();
  assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'directory-key', retry: { maxAttempts: 1, backoffMs: 0 } });
  await assert.rejects(AppsService.attachGlobalAppTarget({ slug: 'global-shop', region: 'eu-fsn', requestBody: {
    regional_app_slug: 'regional-shop', regional_api_key: 'regional-key',
  } }));
  assert.equal(redirected, 0);
  assert.deepEqual(JSON.parse(body), { regional_app_slug: 'regional-shop', regional_api_key: 'regional-key' });
});

for (const source of [false, true]) {
  test(`immutable regional ${source ? 'source' : 'image'} deployments refuse redirects`, async (t) => {
    let redirected = 0;
    let body = '';
    const cellId = '7c93ae23-83ed-4dc9-987a-4676cd3ca6c2';
    const appId = '6cb3f3a7-8b0d-4f6c-82d9-2c2b61ad1983';
    const server = createServer((req, res) => {
      void (async () => {
        if (req.url === '/recipient') { redirected++; res.end('{}'); return; }
        assert.equal(req.url, `/v1/cells/${cellId}/apps/${appId}/deployments`);
        assert.equal(req.headers.authorization, 'Bearer regional-key');
        assert.equal(req.headers['idempotency-key'], 'regional-release-1');
        for await (const chunk of req) body += String(chunk);
        if (source) assert.match(String(req.headers['content-type']), /multipart\/form-data; boundary=/);
        res.writeHead(307, { Location: '/recipient' });
        res.end();
      })().catch(error => res.destroy(error));
    });
    server.listen(0, '127.0.0.1');
    await once(server, 'listening');
    t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
    const address = server.address();
    assert.ok(address && typeof address !== 'string');
    new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'regional-key', retry: { maxAttempts: 1, backoffMs: 0 } });
    const operation = source
      ? deployCellAppMultipart({ cellId, appId, formData: { source: new Blob(['source-marker']), dockerfile: true }, idempotencyKey: 'regional-release-1' })
      : DeploymentsService.deployCellApp({ cellId, appId, requestBody: { image: 'image-marker' }, idempotencyKey: 'regional-release-1' });
    await assert.rejects(operation);
    assert.equal(redirected, 0);
    assert.ok(body.includes(source ? 'source-marker' : 'image-marker'));
  });
}
