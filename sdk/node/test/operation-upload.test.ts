import test from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { GregaleOperations } from '../src/operations-runtime.js';
import type { OperationArtifact } from '../src/customer-operations.js';

const id = '11111111-1111-4111-8111-111111111111', invocation = '22222222-2222-4222-8222-222222222222';
const artifactID = '33333333-3333-4333-8333-333333333333';
const headers = {'X-Gregale-Customer-Operation-Id': id, 'X-Faas-Invocation-Id': invocation,
  'X-Gregale-Operation-Attempt': '1', 'X-Gregale-Operation-Capability': 'a'.repeat(64)};
const input = {report_id: 'csv', name: 'export.csv', data: 'csv'};
const artifact: OperationArtifact = {id: artifactID, uri: `operation://${id}/artifacts/${artifactID}`, name: input.name,
  size_bytes: 3, sha256: 'sha256:'+createHash('sha256').update('csv').digest('hex')};
function runtime(route: (url: URL, init: RequestInit) => Promise<Response> | Response) {
  let identities = 0;
  const instance = new GregaleOperations({apiURL: 'https://api.example.test', identityEndpoint: 'http://127.0.0.1/identity',
    fetch: async (raw, init = {}) => {
      const url = new URL(String(raw));
      if (url.hostname === '127.0.0.1') return Response.json({access_token: `fresh-${++identities}`});
      assert.equal(init.redirect, 'error'); assert.equal(init.credentials, 'omit');
      const proof = init.headers as Record<string, string>;
      assert.equal(proof.Authorization, `Bearer fresh-${identities}`);
      assert.equal(proof['X-Faas-Invocation-Id'], invocation);
      return route(url, init);
    }});
  return {instance, identities: () => identities};
}

test('HTTP uploads snapshot UTF-8 bytes, coalesce and recover a lost acknowledgement with fresh identity', async () => {
  let writes = 0, lookups = 0, retained = false, business = 0;
  const api = runtime((url, init) => {
    if (url.pathname.endsWith('/artifact-upload-receipts')) { lookups++; return Response.json(retained ? {available: true, artifact} : {available: false}); }
    assert.ok(url.pathname.endsWith('/artifact-uploads')); writes++;
    assert.equal((init.headers as Record<string,string>)['Content-Type'], 'application/octet-stream');
    assert.equal(Buffer.from(init.body as Uint8Array).toString(), 'csv');
    assert.equal(url.searchParams.get('sha256'), artifact.sha256);
    assert.equal(url.searchParams.get('size_bytes'), '3');
    retained = true; throw new TypeError('lost acknowledgement');
  });
  await api.instance.runRequest(headers, async () => {
    business++;
    const bytes = Buffer.from('csv');
    const a = api.instance.uploadArtifact({...input, data: bytes});
    const b = api.instance.uploadArtifact(input);
    bytes.fill(0);
    const [first, second] = await Promise.all([a,b]);
    assert.equal(first, second); assert.ok(Object.isFrozen(first));
    assert.equal(await api.instance.uploadArtifact(input), first);
    await assert.rejects(api.instance.uploadArtifact({...input, data: 'changed'}), /different declaration/);
  });
  assert.equal(writes, 1); assert.equal(lookups, 2); assert.equal(business, 1); assert.equal(api.identities(), 3);
});

test('HTTP upload retries private transport only after lookup and rejects conflicting protocol receipts', async () => {
  let writes = 0, lookups = 0;
  const api = runtime(url => {
    if (url.pathname.endsWith('/artifact-upload-receipts')) { lookups++; return Response.json({available: false}); }
    if (++writes === 1) return Response.json({code: 'storage_unavailable'}, {status: 503});
    return Response.json({available: true, artifact});
  });
  await api.instance.runRequest(headers, () => api.instance.uploadArtifact(input));
  assert.equal(writes, 2); assert.equal(lookups, 2);
  for (const response of [{available: false}, {available: true, artifact: {...artifact, sha256: 'changed'}}]) {
    let uploads = 0;
    const bad = runtime(url => url.pathname.endsWith('/artifact-upload-receipts') ? Response.json({available: false}) : (++uploads, Response.json(response)));
    await assert.rejects(Promise.resolve(bad.instance.runRequest(headers, () => bad.instance.uploadArtifact(input))), /receipt/);
    assert.equal(uploads, 1);
  }
});

test('HTTP bounds and finished or detached request contexts cannot upload or reuse cached receipts', async () => {
  let calls = 0;
  const api = runtime(() => { calls++; return Response.json({available: true, artifact}); });
  await assert.rejects(api.instance.uploadArtifact(input), /original request/);
  await api.instance.runRequest(headers, async () => {
    for (const changed of [{...input, maxBytes: 2}, {...input, name: '../csv'}, {...input, report_id: ''}]) {
      await assert.rejects(api.instance.uploadArtifact(changed));
    }
    assert.equal(calls, 0);
    await api.instance.uploadArtifact(input);
    await api.instance.runRequest({...headers}, async () => { await api.instance.uploadArtifact(input); });
    assert.equal(calls, 2); // A second invocation context performs its own lookup.
  });
  let detached!: Promise<void>;
  await api.instance.runRequest(headers, () => {
    detached = new Promise((resolve, reject) => setImmediate(() => {
      assert.rejects(api.instance.uploadArtifact(input), /original request/).then(() => resolve(), reject);
    }));
  });
  await detached; assert.equal(calls, 2);
});

test('HTTP cancellation at upload checkpoint prevents sending bytes or returning success', async () => {
  let reads = 0, writes = 0;
  const deadline = new Date(Date.now()+60000).toISOString();
  const api = runtime(url => {
    if (url.pathname.endsWith('/control')) return Response.json({operation_id: id, invocation_id: invocation, attempt: 1,
      cancellation_requested: ++reads > 1, deadline_at: deadline, lease_expires_at: deadline, observed_at: new Date().toISOString(), poll_after_ms: 1000});
    writes++; return Response.json({available: true, artifact});
  });
  await assert.rejects(api.instance.runCancellableRequest(headers, () => api.instance.uploadArtifact(input)), error =>
    (error as {code: string}).code === 'cancellation_requested');
  assert.equal(writes, 0);
});
