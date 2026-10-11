import test from 'node:test';
import assert from 'node:assert/strict';
import { setTimeout as sleep } from 'node:timers/promises';
import { GregaleWorkflowOperations, OperationStoppedError, type OperationHandlerScope, type OperationWorkflowControlResponse } from '../src/operations-runtime.js';
import type { OperationArtifact, OperationArtifactReport } from '../src/customer-operations.js';

const id = '11111111-1111-4111-8111-111111111111', run = '22222222-2222-4222-8222-222222222222';
const nonce = '33333333-3333-4333-8333-333333333333';
const headers = { 'X-Gregale-Customer-Operation-Id': id, 'X-Gregale-Operation-Execution-Kind': 'workflow',
  'X-Gregale-Operation-Workflow-Run-Id': run, 'X-Gregale-Operation-Workflow-Step': 'finish',
  'X-Gregale-Operation-Generation': '1', 'X-Gregale-Operation-Attempt': '1', 'X-Gregale-Operation-Workflow-Capability': nonce };
const input = {name: 'export.csv', report_id: 'csv', uri: `obj://${id}/${run}/export.csv`, data: 'csv', maxBytes: 10};
const stopped = (code: string) => (error: unknown): boolean => error instanceof OperationStoppedError && error.code === code;
function fixture(options: { budget?: number; read?: (value: OperationWorkflowControlResponse, count: number, signal: AbortSignal) => Response | Promise<Response> } = {}) {
  const deadline = Date.now() + (options.budget ?? 60000);
  let identities = 0, reads = 0, prepares = 0, live = true, retained: OperationArtifact | undefined;
  let onRetain = (): void => {};
  const runtime = new GregaleWorkflowOperations({apiURL: 'https://api.example.test', identityEndpoint: 'http://127.0.0.1/identity', fetch: async (url, init) => {
    if (new URL(String(url)).hostname === '127.0.0.1') return Response.json({access_token: `identity-${++identities}`});
    const proof = new Headers(init?.headers);
    assert.equal(proof.get('Authorization'), `Bearer identity-${identities}`);
    assert.equal(proof.get('X-Gregale-Operation-Execution-Kind'), 'workflow');
    assert.equal(proof.get('X-Gregale-Operation-Workflow-Run-Id'), run);
    assert.equal(proof.get('X-Gregale-Operation-Workflow-Capability'), nonce);
    assert.equal(proof.has('X-Faas-Invocation-Id'), false);
    assert.equal(proof.has('X-Gregale-Operation-Capability'), false);
    assert.equal(init?.redirect, 'error'); assert.equal(init?.cache, 'no-store');
    if (String(url).endsWith('/control')) {
      assert.equal(init?.method, 'GET'); assert.equal(init?.body, undefined);
      if (!live) return Response.json({code: 'operation_stale_attempt'}, {status: 409});
      const value: OperationWorkflowControlResponse = {operation_id: id, workflow_run_id: run, workflow_step: proof.get('X-Gregale-Operation-Workflow-Step')!,
        generation: Number(proof.get('X-Gregale-Operation-Generation')), attempt: Number(proof.get('X-Gregale-Operation-Attempt')),
        cancellation_requested: false, observed_at: new Date().toISOString(), deadline_at: new Date(deadline).toISOString(),
        lease_expires_at: new Date(deadline).toISOString(), poll_after_ms: 100};
      reads++; return options.read ? options.read(value, reads, init!.signal!) : Response.json(value);
    }
    if (!live) return Response.json({code: 'operation_stale_attempt'}, {status: 409});
    if (String(url).endsWith('/artifact-receipts')) return Response.json(retained ? {available: true, artifact: retained} : {available: false});
    const declaration = JSON.parse(String(init?.body)) as OperationArtifactReport;
    prepares++; retained = {id, name: declaration.name, uri: declaration.uri, size_bytes: declaration.size_bytes, sha256: declaration.sha256};
    onRetain(); return Response.json({available: true, artifact: retained});
  }});
  return {runtime, stop: () => {live = false;}, resume: () => {live = true;}, retainHook: (hook: () => void) => {onRetain = hook;}, counts: () => ({identities, reads, prepares})};
}

test('all workflow actions get a private cancellable scope with fresh control reads', async () => {
  const f = fixture(); let saved!: OperationHandlerScope;
  assert.equal(await f.runtime.runCancellableRequest({...headers, 'X-Gregale-Operation-Workflow-Step': 'collect'}, async scope => {
    saved = scope; assert.equal(scope.signal.aborted, false); assert.equal('capability' in f.runtime.context()!, false);
    await Promise.all([scope.checkpoint(), scope.checkpoint()]); return 'rows';
  }), 'rows');
  assert.deepEqual(f.counts(), {identities: 3, reads: 3, prepares: 0});
  assert.equal(saved.signal.aborted, true);
  await assert.rejects(f.runtime.control(), /original trusted request/);
});

for (const field of ['operation_id', 'workflow_run_id', 'workflow_step', 'generation', 'attempt'] as const) {
  test(`control rejects substituted ${field} before entering business code`, async () => {
    const f = fixture({read: value => Response.json({...value, [field]: typeof value[field] === 'number' ? 9 : 'foreign'})});
    let entered = false;
    await assert.rejects(f.runtime.runCancellableRequest(headers, () => {entered = true;}), stopped('execution_authority_lost'));
    assert.equal(entered, false);
  });
}

test('cancellation observed between work units prevents further writes and a successful result', async () => {
  const f = fixture({read: (value, count) => Response.json({...value, cancellation_requested: count > 1})});
  let writes = 0;
  await assert.rejects(f.runtime.runCancellableRequest(headers, async scope => {
    const file = f.runtime.prepareArtifact(input);
    await assert.rejects(scope.checkpoint(), stopped('cancellation_requested'));
    await assert.rejects(file.uploadAndAttach(async () => {writes++;}), stopped('cancellation_requested'));
    return 'ignored cancellation';
  }), stopped('cancellation_requested'));
  assert.equal(writes, 0);
});

test('native authority loss aborts a pending upload and never attaches or retries its uncertain write', async () => {
  const f = fixture(); let writes = 0;
  await assert.rejects(f.runtime.runCancellableRequest(headers, async scope => {
    const file = f.runtime.prepareArtifact(input);
    await file.uploadAndAttach(async upload => {
      writes++; assert.equal(upload.signal, scope.signal); f.stop();
      await sleep(10000, undefined, {signal: upload.signal});
    });
  }), stopped('execution_authority_lost'));
  assert.equal(writes, 1); assert.equal(f.counts().prepares, 0);
});

test('the local fixed deadline stops pending work even when subsequent control reads cannot succeed', async () => {
  const f = fixture({budget: 180, read: async (value, count, signal) => {
    if (count > 1) await sleep(10000, undefined, {signal}); return Response.json(value);
  }});
  await assert.rejects(f.runtime.runCancellableRequest(headers, async scope => {
    await sleep(10000, undefined, {signal: scope.signal});
  }), stopped('deadline_exceeded'));
  assert.equal(f.counts().reads, 2);
});

test('cancellation after a verified prepare retains the receipt for approved resume without another upload', async () => {
  const f = fixture(); let writes = 0;
  f.retainHook(f.stop);
  await assert.rejects(f.runtime.runCancellableRequest(headers, async () => {
    return f.runtime.prepareArtifact(input).uploadAndAttach(async () => {writes++;});
  }), stopped('execution_authority_lost'));
  f.resume();
  const copy = await f.runtime.runCancellableRequest({...headers, 'X-Gregale-Operation-Generation': '2', 'X-Gregale-Operation-Attempt': '2'}, async () => {
    return f.runtime.prepareArtifact(input).uploadAndAttach(async () => {writes++;});
  });
  assert.equal(copy.id, id); assert.equal(writes, 1); assert.equal(f.counts().prepares, 1);
});

test('control failure is closed before business code and workflow scope requires native authority', async () => {
  const f = fixture({read: () => new Response(null, {status: 503})});
  await assert.rejects(f.runtime.runCancellableRequest(headers, () => assert.fail('entered handler')), stopped('control_unavailable'));
  await assert.rejects(f.runtime.runCancellableRequest({}, () => {}), /original trusted request/);
});
