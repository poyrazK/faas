import test from 'node:test';
import assert from 'node:assert/strict';
import { GregaleWorkflowOperations, GregaleOperations } from '../src/operations-runtime.js';
import type { OperationArtifact, OperationArtifactReport } from '../src/customer-operations.js';
import type { PreparedOperationArtifact } from '../src/operation-artifact.js';

const id = '11111111-1111-4111-8111-111111111111', run = '22222222-2222-4222-8222-222222222222';
const capability = '33333333-3333-4333-8333-333333333333', artifactID = '44444444-4444-4444-8444-444444444444';
const headers = { 'X-Gregale-Customer-Operation-Id': id, 'X-Gregale-Operation-Execution-Kind': 'workflow', 'X-Gregale-Operation-Workflow-Run-Id': run,
  'X-Gregale-Operation-Workflow-Step': 'finish', 'X-Gregale-Operation-Generation': '1', 'X-Gregale-Operation-Attempt': '1', 'X-Gregale-Operation-Workflow-Capability': capability };
const input = { name: 'export.csv', report_id: 'csv', uri: `obj://${id}/${run}/exports/export.csv`, data: 'id,value\n1,π\n', maxBytes: 1024 };
function setup() {
  let identity = 0, lookups = 0, prepares = 0, retained: OperationArtifact | undefined, lose = false, rejectLookup = false;
  const generations: string[] = [];
  const options = { apiURL: 'https://api.example.test', identityEndpoint: 'http://127.0.0.1/identity', fetch: (async (url: string | URL | Request, init?: RequestInit): Promise<Response> => {
    const target = new URL(String(url));
    if (target.hostname === '127.0.0.1') return Response.json({ access_token: `workload-${++identity}` });
    const proof = new Headers(init?.headers), report = JSON.parse(String(init?.body)) as OperationArtifactReport;
    assert.equal(proof.get('Authorization'), `Bearer workload-${identity}`);
    assert.equal(proof.get('X-Gregale-Operation-Workflow-Run-Id'), run); assert.equal(proof.get('X-Gregale-Operation-Workflow-Capability'), capability);
    assert.equal(proof.has('X-Faas-Invocation-Id'), false); assert.equal(proof.has('X-Gregale-Operation-Capability'), false);
    generations.push(proof.get('X-Gregale-Operation-Generation')!);
    assert.equal(init?.redirect, 'error'); assert.equal(init?.cache, 'no-store');
    if (target.pathname.endsWith('/artifact-receipts')) {
      lookups++; if (rejectLookup) return Response.json({ code: 'not_found' }, { status: 404 });
      return Response.json(retained ? { available: true, artifact: retained } : { available: false });
    }
    assert.equal(target.pathname, `/v1/runtime/workflow-operations/${id}/artifacts`);
    prepares++; retained = { id: artifactID, name: report.name, uri: report.uri, size_bytes: report.size_bytes, sha256: report.sha256 };
    if (lose) { lose = false; throw Error('prepare response lost'); }
    return Response.json({ available: true, artifact: retained });
  }) as typeof fetch };
  return { runtime: new GregaleWorkflowOperations(options), options, counts: () => ({ identity, lookups, prepares, generations }),
    loseResponse: () => { lose = true; }, rejectLookup: (value: boolean) => { rejectLookup = value; } };
}

test('a lost prepare response and approved fresh request resume reuse one verified file without another write', async () => {
  const api = setup(); let writes = 0;
  await api.runtime.runRequest(headers, async () => {
    assert.equal('capability' in api.runtime.context()!, false);
    const file = api.runtime.prepareArtifact(input); api.loseResponse();
    await assert.rejects(file.uploadAndAttach(async () => { writes++; }), /response lost/);
    assert.equal((await file.uploadAndAttach(async () => { writes++; })).id, artifactID);
  });
  await api.runtime.runRequest({ ...headers, 'X-Gregale-Operation-Generation': '2', 'X-Gregale-Operation-Attempt': '2' }, async () => {
    assert.equal((await api.runtime.prepareArtifact(input).uploadAndAttach(async () => { writes++; })).id, artifactID);
  });
  assert.equal(writes, 1); assert.deepEqual(api.counts(), { identity: 4, lookups: 3, prepares: 1, generations: ['1', '1', '1', '2'] });
});

test('authorization failure during preflight never invokes the bucket writer', async () => {
  const api = setup(); let writes = 0; api.rejectLookup(true);
  await api.runtime.runRequest(headers, async () => {
    const file = api.runtime.prepareArtifact(input);
    await assert.rejects(file.uploadAndAttach(async () => { writes++; })); assert.equal(writes, 0);
    api.rejectLookup(false);
    await file.uploadAndAttach(async () => { writes++; }); assert.equal(writes, 1);
  });
});

test('an uncertain bucket write is never automatically repeated by the prepared artifact', async () => {
  const api = setup(); let writes = 0;
  await api.runtime.runRequest(headers, async () => {
    const file = api.runtime.prepareArtifact(input);
    await assert.rejects(file.uploadAndAttach(async () => { writes++; throw Error('write outcome unknown'); }));
    await file.uploadAndAttach(async () => { writes++; }); assert.equal(writes, 1);
  });
});

test('concurrent requests coalesce and detached callbacks lose workflow artifact authority', async () => {
  const api = setup(); let writes = 0, file!: PreparedOperationArtifact<OperationArtifact>;
  await api.runtime.runRequest(headers, async () => {
    file = api.runtime.prepareArtifact(input);
    const pending = file.uploadAndAttach(async () => { writes++; });
    assert.equal(file.attach(), pending); assert.equal(file.uploadAndAttach(async () => { writes++; }), pending);
    await pending;
  });
  await assert.rejects(file.attach(), /original trusted request/);
  await api.runtime.runRequest(headers, async () => { await assert.rejects(file.attach(), /original trusted request/); });
  assert.equal(writes, 1);
});

test('workflow and HTTP contexts cannot be substituted and stable report identity is required', () => {
  const api = setup(), http = new GregaleOperations(api.options);
  assert.throws(() => http.runRequest(headers, () => {}));
  for (const change of [{ 'X-Gregale-Operation-Capability': 'a'.repeat(64) }, { 'X-Faas-Invocation-Id': run }, { 'X-Faas-Workflow-Attempt': '9' }, { 'X-Gregale-Operation-Generation': '0' }, { 'X-Gregale-Operation-Workflow-Capability': '' }]) {
    assert.throws(() => api.runtime.runRequest({ ...headers, ...change }, () => {}));
  }
  api.runtime.runRequest(headers, () => { assert.throws(() => api.runtime.prepareArtifact({ ...input, report_id: '' })); });
});

function directSetup() {
  let identities = 0, lookups = 0, writes = 0, retained: OperationArtifact | undefined;
  let lost = false, reject = false, wrong = false;
  const runtime = new GregaleWorkflowOperations({apiURL: 'https://api.example.test', identityEndpoint: 'http://127.0.0.1/identity', fetch: async (raw, init) => {
    const url = new URL(String(raw));
    if (url.hostname === '127.0.0.1') return Response.json({access_token: `fresh-${++identities}`});
    const proof = new Headers(init?.headers);
    assert.equal(proof.get('Authorization'), `Bearer fresh-${identities}`);
    assert.equal(proof.get('X-Gregale-Operation-Workflow-Run-Id'), run);
    assert.equal(proof.get('X-Gregale-Operation-Workflow-Capability'), capability);
    assert.equal(proof.get('X-Faas-Invocation-Id'), null);
    assert.equal(init?.redirect, 'error'); assert.equal(init?.credentials, 'omit');
    if (reject) return Response.json({code: 'operation_stale_attempt'}, {status: 409});
    if (url.pathname.endsWith('/artifact-upload-receipts')) {
      lookups++; return Response.json(retained ? {available: true, artifact: retained} : {available: false});
    }
    assert.ok(url.pathname.endsWith('/artifact-uploads')); writes++;
    assert.equal(proof.get('Content-Type'), 'application/octet-stream');
    assert.equal(Buffer.from(init!.body as Uint8Array).toString(), input.data);
    retained = {id: artifactID, name: input.name, uri: `operation://${id}/artifacts/${artifactID}`,
      size_bytes: Buffer.byteLength(input.data), sha256: url.searchParams.get('sha256')!};
    if (wrong) return Response.json({available: true, artifact: {...retained, name: 'changed.csv'}});
    if (!lost) { lost = true; throw new TypeError('lost private-upload reply'); }
    return Response.json({available: true, artifact: retained});
  }});
  return {runtime, counts: () => ({identities, lookups, writes}), reject: () => {reject = true;}, wrong: () => {wrong = true;}};
}

test('direct workflow uploads coalesce, snapshot bytes and reuse the same receipt across an approved resume', async () => {
  const api = directSetup();
  const data = {report_id: input.report_id, name: input.name, data: input.data};
  await api.runtime.runRequest(headers, async () => {
    const bytes = Buffer.from(input.data);
    const first = api.runtime.uploadArtifact({...data, data: bytes});
    const second = api.runtime.uploadArtifact(data); bytes.fill(0);
    const [a,b] = await Promise.all([first,second]); assert.equal(a,b); assert.ok(Object.isFrozen(a));
    assert.equal(await api.runtime.uploadArtifact(data), a);
    await assert.rejects(api.runtime.uploadArtifact({...data, data: 'different'}), /different declaration/);
  });
  await api.runtime.runRequest({...headers, 'X-Gregale-Operation-Generation': '2', 'X-Gregale-Operation-Attempt': '2'}, async () => {
    assert.equal((await api.runtime.uploadArtifact(data)).id, artifactID);
  });
  assert.deepEqual(api.counts(), {identities: 4, lookups: 3, writes: 1});
});

test('direct workflow upload bounds, authorization and protocol failures stop before another transfer', async () => {
  const api = directSetup(), data = {report_id: input.report_id, name: input.name, data: input.data};
  await api.runtime.runRequest(headers, async () => {
    await assert.rejects(api.runtime.uploadArtifact({...data, maxBytes: 1}), /memory bound/);
    await assert.rejects(api.runtime.uploadArtifact({...data, report_id: ''}), /declaration/);
    assert.equal(api.counts().identities, 0);
    api.reject(); await assert.rejects(api.runtime.uploadArtifact(data)); assert.equal(api.counts().writes, 0);
  });
  const wrong = directSetup(); wrong.wrong();
  await assert.rejects(Promise.resolve(wrong.runtime.runRequest(headers, () => wrong.runtime.uploadArtifact(data))), /receipt/);
  assert.equal(wrong.counts().writes, 1);
});

test('finished workflow requests cannot upload or reuse cached files through detached callbacks', async () => {
  const api = directSetup(); let detached!: Promise<void>;
  await api.runtime.runRequest(headers, () => {
    detached = new Promise((resolve,reject) => setImmediate(() => {
      assert.rejects(api.runtime.uploadArtifact({report_id: 'csv', name: input.name, data: input.data}), /original trusted request/).then(() => resolve(),reject);
    }));
  });
  await detached; assert.equal(api.counts().identities, 0);
});
