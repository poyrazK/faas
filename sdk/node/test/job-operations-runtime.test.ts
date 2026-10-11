import assert from 'node:assert/strict';
import test from 'node:test';
import { runJobOperation, type JobOperationScope } from '../src/job-operations-runtime.js';
import { createHash } from 'node:crypto';
import { OperationStoppedError } from '../src/operation-control.js';
import type { PreparedOperationArtifact } from '../src/operation-artifact.js';
import type { OperationArtifact } from '../src/customer-operations.js';

const id = '11111111-1111-4111-8111-111111111111', runID = '22222222-2222-4222-8222-222222222222', instanceID = '33333333-3333-4333-8333-333333333333';
const accountID="44444444-4444-4444-8444-444444444444",appID="55555555-5555-4555-8555-555555555555",platformTenantID="66666666-6666-4666-8666-666666666666";
const capability = 'a'.repeat(64);
const env = { GREGALE_CUSTOMER_OPERATION_ACCOUNT_ID:accountID,GREGALE_CUSTOMER_OPERATION_APP_ID:appID,GREGALE_CUSTOMER_OPERATION_PLATFORM_TENANT_ID:platformTenantID,GREGALE_CUSTOMER_OPERATION_SCOPE:"production", GREGALE_CUSTOMER_OPERATION_ID: id, GREGALE_RUN_ID: runID, GREGALE_CUSTOMER_OPERATION_JOB_INSTANCE_ID: instanceID,
  GREGALE_CUSTOMER_OPERATION_GENERATION: '1', GREGALE_TASK_ATTEMPT: '1', GREGALE_TASK_INDEX: '0', GREGALE_CUSTOMER_OPERATION_JOB_CAPABILITY: capability,
  GREGALE_CUSTOMER_OPERATION_INPUT: '{"count":1}' };
const expiry=new Date(Date.now()+60000).toISOString();
const control = () => ({ account_id:accountID,app_id:appID,platform_tenant_id:platformTenantID,scope:"production", operation_id: id, job_run_id: runID, generation: 1, attempt: 1, cancellation_requested: false,
  deadline_at:expiry, lease_expires_at:expiry, observed_at: new Date().toISOString(), poll_after_ms: 1000 });

test('Job Operation prepares a result after one handler execution', async () => {
  const calls: string[] = [];
  const fetchImpl: typeof fetch = async (url, init) => {
    const path = new URL(String(url)).pathname; calls.push(path);
    const headers = new Headers(init?.headers);
    assert.equal(headers.get('authorization'), null);
    assert.equal(headers.get('X-Gregale-Operation-Job-Capability'), capability);
    assert.equal(init?.redirect, 'error');
    return Response.json(path.endsWith('/control') ? control() : { id, state: 'running' });
  };
  let executed = 0;
  const result = await runJobOperation<{count: number}, {file: string}>({ apiURL: 'https://api.example.test', env, fetch: fetchImpl }, async (input, scope) => {
    executed++; assert.equal(input.count, 1);
    assert.deepEqual(scope.operation, { accountID,appID,platformTenantID,scope:"production",id, runID, generation: 1, attempt: 1 });
    assert.ok(!JSON.stringify(scope).includes(capability));
    await scope.progress({ report_id: 'generating', stage: 'generating', completed: 1, total: 1 });
    return { file: 'obj://export.csv' };
  });
  assert.equal(executed, 1); assert.equal(result.file, 'obj://export.csv');
  assert.equal(calls.filter(x => x.endsWith('/result')).length, 1);
});

test('Lost result response retries the same receipt without repeating work', async () => {
  let writes = 0, executed = 0; const bodies: string[] = [];
  const fetchImpl: typeof fetch = async (url, init) => {
    if (String(url).endsWith('/control')) return Response.json(control());
    bodies.push(String(init?.body)); if (++writes === 1) throw new TypeError('connection reset');
    return Response.json({ id, state: 'running' });
  };
  await runJobOperation({ apiURL: 'http://localhost', env, fetch: fetchImpl }, async () => { executed++; return { file: 'export.csv' }; });
  assert.equal(executed, 1); assert.equal(writes, 2); assert.equal(bodies[0], bodies[1]);
});

test('Cancellation prevents business entry', async () => {
  let executed = 0;
  await assert.rejects(runJobOperation({ apiURL: 'http://localhost', env, fetch: async () => Response.json({ ...control(), cancellation_requested: true }) }, async () => { executed++; }),
    (error: unknown) => error instanceof OperationStoppedError && error.code === 'cancellation_requested');
  assert.equal(executed, 0);
});

test('Replacement generation prevents result submission', async () => {
  let replaced = false, results = 0;
  const fetchImpl: typeof fetch = async url => {
    if (String(url).endsWith('/result')) results++;
    return Response.json({ ...control(), generation: replaced ? 2 : 1 });
  };
  await assert.rejects(runJobOperation({ apiURL: 'http://localhost', env, fetch: fetchImpl }, async () => { replaced = true; return { file: 'stale.csv' }; }), OperationStoppedError);
  assert.equal(results, 0);
});

test('Business failure does not prepare success', async () => {
  let results = 0;
  await assert.rejects(runJobOperation({ apiURL: 'http://localhost', env, fetch: async url => {
    if (String(url).endsWith('/result')) results++; return Response.json(control());
  } }, async () => { throw new Error('provider outcome unknown'); }), /provider outcome unknown/);
  assert.equal(results, 0);
});

test('Missing task capability fails before networking', async () => {
  let calls = 0;
  await assert.rejects(runJobOperation({ apiURL: 'http://localhost', env: { ...env, GREGALE_CUSTOMER_OPERATION_JOB_CAPABILITY: '' }, fetch: async () => { calls++; return Response.json(control()); } }, async () => {}), /trusted Job Operation context/);
  assert.equal(calls, 0);
});

test('Customer identity is verified against the leased task', async () => {
  let executed = 0;
  await assert.rejects(runJobOperation({ apiURL: 'http://localhost', env: { ...env, GREGALE_CUSTOMER_OPERATION_PLATFORM_TENANT_ID: id }, fetch: async () => Response.json(control()) }, async () => { executed++; }), OperationStoppedError);
  assert.equal(executed, 0);
});

const file = { report_id: 'csv', name: 'export.csv', uri: `obj://${appID}/${accountID}/export.csv`, data: 'csv', maxBytes: 3 };

test('Job file preparation coalesces uploads and replays a lost acknowledgement', async () => {
  let uploads = 0, preparations = 0, receipts = 0;
  const bodies: string[] = [];
  let escaped: PreparedOperationArtifact<OperationArtifact> | undefined;
  const fetchImpl: typeof fetch = async (url, init) => {
    const path = new URL(String(url)).pathname;
    const headers = new Headers(init?.headers);
    assert.equal(headers.get('authorization'), null);
    assert.equal(headers.get('X-Gregale-Operation-Job-Capability'), capability);
    assert.equal(init?.redirect, 'error'); assert.equal(init?.credentials, 'omit');
    if (path.endsWith('/control')) return Response.json(control());
    if (path.endsWith('/artifact-receipts')) { receipts++; return Response.json({ available: false }); }
    if (path.endsWith('/artifacts')) {
      preparations++; bodies.push(String(init?.body));
      if (preparations === 1) throw new TypeError('lost prepare response');
      const { report_id: _, ...declaration } = JSON.parse(String(init?.body));
      return Response.json({ available: true, artifact: { id, ...declaration } });
    }
    return Response.json({ id, state: 'running' });
  };
  await runJobOperation({ apiURL: 'http://localhost', env, fetch: fetchImpl }, async (_input, scope) => {
    const prepared = scope.prepareArtifact(file); escaped = prepared;
    const write = async ({ bytes, report, signal }: { bytes: Uint8Array; report: unknown; signal?: AbortSignal }) => {
      uploads++; assert.equal(Buffer.from(bytes).toString(), 'csv');
      assert.ok(!JSON.stringify(report).includes(capability)); assert.ok(!signal?.aborted);
    };
    const [first, second] = await Promise.all([prepared.uploadAndAttach(write), prepared.uploadAndAttach(write)]);
    assert.equal(first.id, second.id);
    await prepared.uploadAndAttach(write);
    return { file: first.id };
  });
  assert.equal(uploads, 1); assert.equal(receipts, 1); assert.equal(preparations, 2); assert.equal(bodies[0], bodies[1]);
  await assert.rejects(escaped!.attach(), OperationStoppedError);
});

test('Job file receipt avoids writing the mutable source again', async () => {
  let uploads = 0, preparations = 0;
  const fetchImpl: typeof fetch = async (url, init) => {
    if (String(url).endsWith('/control')) return Response.json(control());
    if (String(url).endsWith('/artifact-receipts')) {
      const { report_id: _, ...declaration } = JSON.parse(String(init?.body));
      return Response.json({ available: true, artifact: { id, ...declaration } });
    }
    if (String(url).endsWith('/artifacts')) preparations++;
    return Response.json({ id, state: 'running' });
  };
  await runJobOperation({ apiURL: 'http://localhost', env, fetch: fetchImpl }, async (_input, scope) => {
    const artifact = await scope.prepareArtifact(file).uploadAndAttach(async () => { uploads++; });
    assert.equal(artifact.id, id); return { file: artifact.id };
  });
  assert.equal(uploads, 0); assert.equal(preparations, 0);
});

test('Job file preparation rechecks the generation before the bucket writer', async () => {
  let replaced = false, uploads = 0;
  await assert.rejects(runJobOperation({ apiURL: 'http://localhost', env, fetch: async () => Response.json({ ...control(), generation: replaced ? 2 : 1 }) }, async (_input, scope) => {
    const prepared = scope.prepareArtifact(file); replaced = true;
    await prepared.uploadAndAttach(async () => { uploads++; });
    return { file: id };
  }), OperationStoppedError);
  assert.equal(uploads, 0);
});

test('Job file helper rejects conflicting receipts before writing', async () => {
  let uploads = 0;
  await assert.rejects(runJobOperation({ apiURL: 'http://localhost', env, fetch: async (url, init) => {
    if (String(url).endsWith('/control')) return Response.json(control());
    return Response.json({ available: true, artifact: { id, ...JSON.parse(String(init?.body)), sha256: 'sha256:' + '0'.repeat(64) } });
  } }, async (_input, scope) => {
    await scope.prepareArtifact(file).uploadAndAttach(async () => { uploads++; });
    return { file: id };
  }), /declaration changed/);
  assert.equal(uploads, 0);
});

const directID = '77777777-7777-4777-8777-777777777777';
const directArtifact = (data: string, name = 'export.csv'): OperationArtifact => ({id: directID, uri: `operation://${id}/artifacts/${directID}`,
  name, size_bytes: Buffer.byteLength(data), sha256: 'sha256:' + createHash('sha256').update(data).digest('hex')});

test('direct uploads snapshot bytes, coalesce calls and reconcile a lost acknowledgement', async () => {
  let writes = 0, lookups = 0, executed = 0, retained: OperationArtifact | undefined, escaped: JobOperationScope | undefined;
  const fetchImpl: typeof fetch = async (value, init) => {
    const url = new URL(String(value)), headers = new Headers(init?.headers);
    assert.equal(headers.get('authorization'), null); assert.equal(headers.get('X-Gregale-Operation-Job-Capability'), capability);
    assert.equal(init?.redirect, 'error'); assert.equal(init?.credentials, 'omit');
    if (url.pathname.endsWith('/control')) return Response.json(control());
    if (url.pathname.endsWith('/artifact-upload-receipts')) { lookups++; return Response.json(retained ? {available: true, artifact: retained} : {available: false}); }
    if (url.pathname.endsWith('/artifact-uploads')) {
      writes++;
      assert.equal(headers.get('content-type'), 'application/octet-stream');
      assert.equal(Buffer.from(init!.body as Uint8Array).toString(), 'csv');
      assert.equal(url.searchParams.get('sha256'), directArtifact('csv').sha256);
      assert.equal(url.searchParams.get('report_id'), 'csv');
      retained = directArtifact('csv'); throw new TypeError('lost upload response');
    }
    return Response.json({id, state: 'running'});
  };
  await runJobOperation({apiURL: 'http://localhost', env, fetch: fetchImpl}, async (_input, scope) => {
    executed++; escaped = scope;
    const bytes = Buffer.from('csv');
    const first = scope.uploadArtifact({report_id: 'csv', name: 'export.csv', data: bytes});
    const second = scope.uploadArtifact({report_id: 'csv', name: 'export.csv', data: bytes});
    bytes.fill(0);
    const [a, b] = await Promise.all([first, second]); assert.equal(a, b); assert.ok(Object.isFrozen(a));
    assert.equal(await scope.uploadArtifact({report_id: 'csv', name: 'export.csv', data: 'csv'}), a);
    await assert.rejects(scope.uploadArtifact({report_id: 'csv', name: 'export.csv', data: 'changed'}), /different declaration/);
    return {file: a.id};
  });
  assert.equal(executed, 1); assert.equal(writes, 1); assert.equal(lookups, 2);
  await assert.rejects(escaped!.uploadArtifact({report_id: 'csv', name: 'export.csv', data: 'csv'}), OperationStoppedError);
});

test('interrupted platform writes retry the same declaration after a fresh receipt lookup', async () => {
  let writes = 0, lookups = 0, executed = 0;
  const declarations: string[] = [];
  const fetchImpl: typeof fetch = async (value) => {
    const url = new URL(String(value));
    if (url.pathname.endsWith('/control')) return Response.json(control());
    if (url.pathname.endsWith('/artifact-upload-receipts')) { lookups++; return Response.json({available: false}); }
    if (url.pathname.endsWith('/artifact-uploads')) {
      declarations.push(url.search); if (++writes === 1) throw new TypeError('transfer interrupted');
      return Response.json({available: true, artifact: directArtifact('csv')});
    }
    return Response.json({id, state: 'running'});
  };
  await runJobOperation({apiURL: 'http://localhost', env, fetch: fetchImpl}, async (_input, scope) => {
    executed++; return {file: (await scope.uploadArtifact({report_id: 'csv', name: 'export.csv', data: 'csv'})).id};
  });
  assert.equal(executed, 1); assert.equal(writes, 2); assert.equal(lookups, 2); assert.equal(declarations[0], declarations[1]);
});

test('direct upload protocol errors stop without repeating a write or preparing success', async () => {
  for (const receipt of [{available: false}, {available: true, artifact: {...directArtifact('csv'), uri: 'operation://other/artifacts/'+directID}},
    {available: true, artifact: {...directArtifact('csv'), sha256: 'sha256:'+'0'.repeat(64)}}, {artifact: directArtifact('csv')}]) {
    let writes = 0, results = 0;
    await assert.rejects(runJobOperation({apiURL: 'http://localhost', env, fetch: async value => {
      const path = new URL(String(value)).pathname;
      if (path.endsWith('/control')) return Response.json(control());
      if (path.endsWith('/artifact-upload-receipts')) return Response.json({available: false});
      if (path.endsWith('/artifact-uploads')) { writes++; return Response.json(receipt); }
      results++; return Response.json({id});
    }}, async (_input, scope) => { await scope.uploadArtifact({report_id: 'csv', name: 'export.csv', data: 'csv'}); return {}; }), /receipt/);
    assert.equal(writes, 1); assert.equal(results, 0);
  }
});

test('upload bounds, receipt conflicts and cancellation fail before bytes are sent', async () => {
  let writes = 0, cancelled = false;
  await runJobOperation({apiURL: 'http://localhost', env, fetch: async value => {
    const path = new URL(String(value)).pathname;
    if (path.endsWith('/control')) return Response.json({...control(), cancellation_requested: cancelled});
    if (path.endsWith('/artifact-upload-receipts')) return Response.json({available: true, artifact: {...directArtifact('csv'), name: 'changed.csv'}});
    if (path.endsWith('/artifact-uploads')) writes++;
    return Response.json({id});
  }}, async (_input, scope) => {
    await assert.rejects(scope.uploadArtifact({report_id: 'csv', name: 'export.csv', data: 'csv', maxBytes: 2}), /memory bound/);
    await assert.rejects(scope.uploadArtifact({report_id: 'csv', name: '../export.csv', data: 'csv'}), /declaration/);
    await assert.rejects(scope.uploadArtifact({report_id: 'csv', name: 'export.csv', data: 'csv'}), /declaration changed/);
    return {};
  });
  await assert.rejects(runJobOperation({apiURL: 'http://localhost', env, fetch: async () => Response.json({...control(), cancellation_requested: cancelled})}, async (_input, scope) => {
    cancelled = true; await scope.uploadArtifact({report_id: 'csv', name: 'export.csv', data: 'csv'}); return {};
  }), OperationStoppedError);
  assert.equal(writes, 0);
});
