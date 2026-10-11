import test from 'node:test';
import assert from 'node:assert/strict';
import { setTimeout as sleep } from 'node:timers/promises';
import { GregaleOperations, OperationStoppedError, type OperationExecutionControlResponse, type OperationHandlerScope } from '../src/operations-runtime.js';

const id = '11111111-1111-4111-8111-111111111111';
const invocation = '22222222-2222-4222-8222-222222222222';
const headers = { 'X-Gregale-Customer-Operation-Id': id, 'X-Faas-Invocation-Id': invocation,
  'X-Gregale-Operation-Attempt': '2', 'X-Gregale-Operation-Capability': 'a'.repeat(64) };
const control = (values: Partial<OperationExecutionControlResponse> = {}): OperationExecutionControlResponse => {
  const now = Date.now();
  return { operation_id: id, invocation_id: invocation, attempt: 2, cancellation_requested: false, observed_at: new Date(now).toISOString(),
    lease_expires_at: new Date(now + 60000).toISOString(), deadline_at: new Date(now + 120000).toISOString(), poll_after_ms: 1000, ...values };
};
const stopped = (code: string) => (error: unknown): boolean => error instanceof OperationStoppedError && error.code === code;
function runtime(read: (call: number, init?: RequestInit) => Promise<Response> | Response) {
  let identities = 0, reads = 0, reports = 0;
  const client = new GregaleOperations({ apiURL: 'https://api.gregale.test', identityEndpoint: 'http://127.0.0.1/identity', fetch: async (url, init) => {
    if (new URL(String(url)).hostname === '127.0.0.1') return Response.json({ access_token: `identity-${++identities}` });
    const proof = new Headers(init?.headers);
    assert.equal(proof.get('Authorization'), `Bearer identity-${identities}`);
    assert.equal(proof.get('X-Faas-Invocation-Id'), invocation);
    assert.equal(proof.get('X-Gregale-Operation-Attempt'), '2');
    assert.equal(proof.get('X-Gregale-Operation-Capability'), 'a'.repeat(64));
    assert.equal(init?.redirect, 'error'); assert.equal(init?.cache, 'no-store');
    if (String(url).endsWith('/control')) { assert.equal(init?.method, 'GET'); assert.equal(init?.body, undefined); return read(++reads, init); }
    reports++; return Response.json({ id, state: 'running' });
  } });
  return { client, counts: () => ({ identities, reads, reports }) };
}

test('control reads refresh workload identity and share a conservative request scope', async () => {
  // A skewed client clock must not reinterpret the server's absolute deadline.
  const observed = Date.now() + 3600000;
  const value = control({ observed_at: new Date(observed).toISOString(), lease_expires_at: new Date(observed + 60000).toISOString(), deadline_at: new Date(observed + 120000).toISOString() });
  const { client, counts } = runtime(() => Response.json(value));
  let saved: OperationHandlerScope | undefined;
  assert.equal(await client.runCancellableRequest(headers, async scope => {
    saved = scope; assert.equal(scope.deadlineAt, value.deadline_at); assert.equal(scope.signal.aborted, false);
    await Promise.all([scope.checkpoint(), scope.checkpoint()]);
    await client.progress({ report_id: 'start', stage: 'generating', completed: 0, total: 1 });
    return 'result';
  }), 'result');
  assert.deepEqual(counts(), { identities: 4, reads: 3, reports: 1 });
  assert.equal(saved!.signal.aborted, true);
  assert.throws(() => saved!.throwIfStopped(), /original request/);
  await assert.rejects(client.control(), /requires an operation execution/);
  await assert.rejects(client.runCancellableRequest({}, () => 'untrusted'), /Trusted/);
});

test('an already requested cancellation prevents entry into business code', async () => {
  const { client, counts } = runtime(() => Response.json(control({ cancellation_requested: true })));
  let work = 0;
  await assert.rejects(client.runCancellableRequest(headers, () => ++work), stopped('cancellation_requested'));
  assert.equal(work, 0); assert.equal(counts().reads, 1);
});

test('final control observation rejects output when cancellation arrives during work', async () => {
  const value = control();
  const { client, counts } = runtime(call => Response.json({ ...value, cancellation_requested: call > 1 }));
  let effects = 0;
  await assert.rejects(client.runCancellableRequest(headers, async () => { effects++; return { csv: 'completed externally' }; }), stopped('cancellation_requested'));
  assert.equal(effects, 1); assert.equal(counts().reads, 2); // The helper never repeats business work.
});

test('polling interrupts signal-aware work and forbids reports and uploads after cancellation', async () => {
  const value = control({ lease_expires_at: new Date(Date.now() + 600).toISOString(), poll_after_ms: 100 });
  const { client, counts } = runtime(call => Response.json({ ...value, cancellation_requested: call > 1 }));
  let writes = 0;
  await assert.rejects(client.runCancellableRequest(headers, async scope => {
    const file = client.prepareArtifact({ name: 'file', uri: `obj://${id}/${invocation}/file`, data: 'bytes', maxBytes: 10 });
    await assert.rejects(sleep(5000, undefined, { signal: scope.signal }));
    await assert.rejects(client.progress({ stage: 'generating', completed: 1, total: 1 }), stopped('cancellation_requested'));
    await assert.rejects(file.uploadAndAttach(async () => { writes++; }), stopped('cancellation_requested'));
    scope.throwIfStopped();
  }), stopped('cancellation_requested'));
  assert.equal(writes, 0); assert.equal(counts().reports, 0); assert.equal(counts().reads, 2);
});

test('deadline and lease expiry abort I/O and prevent a successful return', async () => {
  for (const code of ['deadline_exceeded', 'execution_lease_expired']) {
    const now = Date.now(), expiry = new Date(now + 40).toISOString();
    const value = control({ observed_at: new Date(now).toISOString(), lease_expires_at: expiry,
      deadline_at: code === 'deadline_exceeded' ? expiry : new Date(now + 120000).toISOString() });
    const { client } = runtime(() => Response.json(value));
    await assert.rejects(client.runCancellableRequest(headers, async scope => {
      await assert.rejects(sleep(5000, undefined, { signal: scope.signal }));
      return 'ignored abort';
    }), stopped(code));
  }
});

test('request latency consumes the observed budget before business code starts', async () => {
  const now = Date.now();
  const value = control({ observed_at: new Date(now).toISOString(), lease_expires_at: new Date(now + 10).toISOString() });
  const { client } = runtime(async () => { await sleep(30); return Response.json(value); });
  let work = 0;
  await assert.rejects(client.runCancellableRequest(headers, () => ++work), stopped('execution_lease_expired'));
  assert.equal(work, 0);
});

test('replaying a control snapshot cannot refresh a lease or the admitted deadline', async () => {
  const now = Date.now();
  const value = control({ observed_at: new Date(now).toISOString(), lease_expires_at: new Date(now+120).toISOString(), poll_after_ms: 100 });
  const { client, counts } = runtime(() => Response.json(value));
  await assert.rejects(client.runCancellableRequest(headers, async scope => {
    for (let i = 0; i < 10; i++) { await sleep(30); await scope.checkpoint(); }
    return 'extended by stale observations';
  }), stopped('execution_lease_expired'));
  assert.ok(counts().reads < 10);
});

test('scheduler renewals cannot extend the initial deadline budget', async () => {
  const original = Date.now, now = original(), deadline = new Date(now+180000).toISOString();
  let local = now, reads = 0;
  const { client } = runtime(() => Response.json(control({ observed_at: new Date(now).toISOString(), deadline_at: deadline,
    // A later lease arrives with an old observation timestamp. The deadline's
    // original duration still cannot be restarted by this response.
    lease_expires_at: new Date(now+(++reads === 1 ? 120000 : 180000)).toISOString() })));
  try {
    Date.now = () => local;
    await assert.rejects(client.runCancellableRequest(headers, async scope => {
      local += 60000; await scope.checkpoint();
      local += 120000; scope.throwIfStopped();
      return 'extended deadline';
    }), stopped('deadline_exceeded'));
    assert.equal(reads, 2);
  } finally { Date.now = original; }
});

test('control loss and forged observations fail closed without exposing remote errors', async () => {
  for (const read of [
    () => Response.json(control({ attempt: 3 })),
    () => Response.json(control({ invocation_id: id })),
    () => Response.json(control({ operation_id: invocation })),
    () => Response.json(control({ deadline_at: 'not-a-date' })),
    () => Response.json({ secret: 'private' }, { status: 409 }),
    () => { throw Error('private connection detail'); },
  ]) {
    const { client } = runtime(read); let work = 0;
    await assert.rejects(client.runCancellableRequest(headers, () => ++work), (error: unknown) => {
      assert.ok(error instanceof OperationStoppedError); assert.doesNotMatch(String(error), /private/); return true;
    });
    assert.equal(work, 0);
  }
});

test('an admitted deadline cannot be replaced by a later control response', async () => {
  const value = control();
  const { client } = runtime(call => Response.json(call === 1 ? value : { ...value, deadline_at: new Date(Date.parse(value.deadline_at) + 60000).toISOString() }));
  await assert.rejects(client.runCancellableRequest(headers, scope => scope.checkpoint()), stopped('execution_authority_lost'));
});

test('finishing a request aborts a pending control read and clears polling', async () => {
  const value = control({ lease_expires_at: new Date(Date.now() + 600).toISOString(), poll_after_ms: 100 });
  let polling!: () => void; const started = new Promise<void>(resolve => { polling = resolve; });
  let aborted = false;
  const { client, counts } = runtime((call, init) => {
    if (call === 1) return Response.json(value);
    polling();
    return new Promise((_resolve, reject) => {
      init!.signal!.addEventListener('abort', () => { aborted = true; reject(init!.signal!.reason); }, { once: true });
    });
  });
  await assert.rejects(client.runCancellableRequest(headers, async () => { await started; throw Error('handler failed'); }), /handler failed/);
  assert.equal(aborted, true);
  await sleep(250); assert.equal(counts().reads, 2);
});

test('a forward clock step stops CPU work without extending budgets on backward steps', async () => {
  const original = Date.now, value = control();
  const { client } = runtime(() => Response.json(value));
  try {
    await assert.rejects(client.runCancellableRequest(headers, scope => {
      Date.now = () => original() - 3600000; scope.throwIfStopped();
      Date.now = () => original() + 3600000; scope.throwIfStopped();
    }), stopped('execution_lease_expired'));
  } finally { Date.now = original; }
});
