import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { AppsService, FaaSClient } from '../src/index.js';

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
