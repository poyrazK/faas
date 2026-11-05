import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { FaaSClient, ProjectsService, type ProjectEnvironmentPromotionSecretPreviewRequest } from '../src/preview.js';

test('production secret preview sends immutable references in a bounded POST body', async (t) => {
  const request: ProjectEnvironmentPromotionSecretPreviewRequest = {
    from_environment: 'staging', sync_config: true, verify: true,
    secrets: { api: { TOKEN: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa' }, web: {} },
  };
  const server = createServer((req, res) => { void (async () => {
    assert.equal(req.method, 'POST');
    assert.equal(req.url, '/v1/projects/shop/environments/production/promotion-preview');
    let body = ''; for await (const chunk of req) body += String(chunk);
    assert.deepEqual(JSON.parse(body), request);
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify({ secrets: { versions: request.secrets, previous_versions: { api: {} } } }));
  })().catch(error => { res.statusCode = 500; res.end(String(error)); }); });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture-token', retry: { maxAttempts: 1, backoffMs: 0 } });
  const response = await ProjectsService.previewProjectEnvironmentPromotionSecrets({ slug: 'shop', environment: 'production', requestBody: request });
  assert.deepEqual(response.secrets?.versions, request.secrets);
});
