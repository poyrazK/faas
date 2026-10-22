import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { FaaSClient, StorageService } from '../src/index.js';

test('organization object storage clients retain tenant and bucket boundaries', async (t) => {
  const paths: string[] = [];
  const server = createServer((req, res) => {
    paths.push(`${req.method} ${req.url}`);
    assert.equal(req.headers.authorization, 'Bearer company-token');
    res.setHeader('Content-Type', 'application/json');
    if (req.method === 'DELETE') res.writeHead(204).end();
    else res.end(JSON.stringify({ items: [] }));
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address();
  assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'company-token', retry: { maxAttempts: 1, backoffMs: 0 } });
  await StorageService.listOrgObjectBuckets({ organization: 'my company', slug: 'app/id' });
  await StorageService.listOrgObjectStorageComputeBindings({ organization: 'my company', slug: 'app/id', bucket: 'bucket/id' });
  await StorageService.revokeOrgObjectS3Credential({ organization: 'my company', slug: 'app/id', bucket: 'bucket/id', credential: 'key/id' });
  assert.deepEqual(paths, [
    'GET /v1/orgs/my%20company/apps/app%2Fid/buckets',
    'GET /v1/orgs/my%20company/apps/app%2Fid/buckets/bucket%2Fid/compute-bindings',
    'DELETE /v1/orgs/my%20company/apps/app%2Fid/buckets/bucket%2Fid/s3-credentials/key%2Fid',
  ]);
});
