import test from 'node:test';
import assert from 'node:assert/strict';
import { GregaleOperationSession, type OperationSessionClient, type OperationSessionUpdate } from '../src/operation-session.js';
import { GregaleOperationClient, OperationHTTPError, type Operation } from '../src/customer-operations.js';

const id = '11111111-1111-4111-8111-111111111111';
const other = '22222222-2222-4222-8222-222222222222';
type Result = { csv: string };
const snapshot: Operation<Result> = {
  id, name: 'export', generation: 1, state: 'succeeded', result: { csv: 'id,value\n1,10\n' },
  completion_delivery: { state: 'failed', attempts: 2 }, cancellation_requested: false,
  latest_sequence: 4, created_at: '2026-10-05T12:00:00Z', updated_at: '2026-10-05T12:01:00Z', expires_at: '2026-10-12T12:01:00Z',
};
const receipt = { id, status_url: `/operations/${id}`, events_url: `/operations/${id}/events` };
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(finish => { resolve = finish; });
  return { promise, resolve };
}
function client(overrides: Partial<OperationSessionClient<Result>> = {}): OperationSessionClient<Result> {
  return {
    start: async () => receipt, list: async () => ({ operations: [] }), get: async selected => ({ ...snapshot, id: selected }),
    async *subscribe() {}, cancel: async () => snapshot, download: async () => new Response('csv'), ...overrides,
  };
}
function session(api: OperationSessionClient<Result>, onChange?: (update: OperationSessionUpdate<Result>) => void) {
  return new GregaleOperationSession({ client: api, appID: id, scope: 'default', definitionID: other, name: 'export', onChange });
}

test('uncertain submission freezes input and shares the retry promise and key', async () => {
  const calls: { input: unknown; key: string }[] = [], accepted = deferred<typeof receipt>();
  const feature = session(client({ start: async (_definition, input, key) => {
    calls.push({ input, key });
    if (calls.length === 1) throw Error('connection lost');
    return accepted.promise;
  } }));
  const input = { count: 1, filter: { b: 2, a: 1 } };
  await assert.rejects(feature.start(input), /connection lost/);
  input.count = 2;
  await assert.rejects(feature.start(input), /same input/);
  await assert.rejects(feature.start({ count: 1, filter: { a: 1, b: 2 } }, 'different-key'), /same input/);
  const first = feature.start({ filter: { a: 1, b: 2 }, count: 1 });
  const second = feature.start({ count: 1, filter: { b: 2, a: 1 } });
  assert.equal(first, second);
  assert.deepEqual(calls[0]?.input, calls[1]?.input);
  assert.equal(calls[0]?.key, calls[1]?.key);
  accepted.resolve(receipt);
  assert.deepEqual(await first, receipt);
  feature.close();
});

test('accepted work survives an unavailable status read without another submission', async () => {
  let starts = 0, update: OperationSessionUpdate<Result> | undefined;
  const feature = session(client({ start: async () => { starts++; return receipt; }, get: async () => { throw Error('status unavailable'); } }), value => { update = value; });
  assert.deepEqual(await feature.start({ count: 1 }), receipt);
  assert.match(update?.error ?? '', /Operation accepted/);
  assert.equal(starts, 1);
  feature.close();
});

test('invalid keys do not poison later submissions and acceptance keeps duplicate-click input bound', async () => {
  const status = deferred<Operation<Result>>(); let starts = 0;
  const feature = session(client({ start: async () => { starts++; return receipt; }, get: async () => status.promise }));
  await assert.rejects(feature.start({count: 1}, ''), /idempotency/);
  await assert.rejects(feature.start({count: 1}, 'x'.repeat(129)), /idempotency/);
  const accepted = feature.start({count: 1}, 'valid');
  await Promise.resolve();
  await assert.rejects(feature.start({count: 2}), /same input/);
  assert.equal(feature.start({count: 1}), accepted);
  status.resolve(snapshot); await accepted;
  assert.equal(starts, 1); feature.close();
});

test('confirmed admission rejection permits corrected input while conflicts preserve request identity', async () => {
  for (const status of [422, 409]) {
    let starts = 0;
    const feature = session(client({ start: async () => {
      if (++starts === 1) throw new OperationHTTPError(status, status === 422 ? 'validation' : 'operation_input_conflict');
      return receipt;
    } }));
    await assert.rejects(feature.start({count: 1001}));
    if (status === 422) { await feature.start({count: 1}); assert.equal(starts, 2); }
    else { await assert.rejects(feature.start({count: 1}), /same input/); assert.equal(starts, 1); }
    feature.close();
  }
});

test('a rejected retry cannot erase uncertainty from an earlier lost response', async () => {
  const keys: string[] = [];
  const feature = session(client({ start: async (_definition, _input, key) => {
    keys.push(key);
    if (keys.length === 1) throw Error('response lost');
    if (keys.length === 2) throw new OperationHTTPError(401, 'unauthorized');
    return receipt;
  } }));
  await assert.rejects(feature.start({count: 1}), /response lost/);
  await assert.rejects(feature.start({count: 1}), /unauthorized/);
  await assert.rejects(feature.start({count: 2}), /same input/);
  await feature.start({count: 1});
  assert.equal(new Set(keys).size, 1); feature.close();
});

test('late submission cannot select work after signout or replace a newer selection', async () => {
  for (const signout of [false, true]) {
    const accepted = deferred<typeof receipt>();
    const feature = session(client({ start: async () => accepted.promise }));
    const pending = feature.start({ count: 1 });
    if (signout) feature.close(); else await feature.open(other);
    accepted.resolve(receipt);
    if (signout) await assert.rejects(pending, /Session closed/);
    else { await pending; assert.equal(feature.selected, other); }
    feature.close();
  }
});

test('selection changes fence delayed snapshots even when transport ignores abort', async () => {
  const stale = deferred<Operation<Result>>();
  const updates: string[] = [];
  const feature = session(client({ get: async selected => selected === id ? stale.promise : { ...snapshot, id: selected } }), value => { if (value.operation) updates.push(value.operation.id); });
  const opening = feature.open(id);
  await feature.open(other);
  stale.resolve(snapshot); await opening;
  assert.deepEqual(updates, [other]); assert.equal(feature.snapshot?.id, other);
  feature.close();
});

test('refresh and cancellation cannot overwrite a different selected operation', async () => {
  for (const action of ['refresh', 'cancel'] as const) {
    const stale = deferred<Operation<Result>>(); let delay = false;
    const api = client({ get: async selected => delay && selected === id ? stale.promise : { ...snapshot, id: selected }, cancel: async () => stale.promise });
    const feature = session(api); await feature.open(id); delay = true;
    const pending = feature[action]();
    // Let refresh complete its history read and enter the selected status read.
    await Promise.resolve(); await feature.open(other);
    stale.resolve(snapshot); await pending;
    assert.equal(feature.snapshot?.id, other); feature.close();
  }
});

test('history uses explicit scope, discards stale pages and deduplicates live pages', async () => {
  const delayed = deferred<{ operations: typeof snapshot[] }>(); let calls = 0;
  const feature = session(client({ list: async options => {
    assert.equal(options.appID, id); assert.equal(options.scope, 'default'); assert.equal(options.name, 'export');
    if (++calls === 1) return delayed.promise;
    if (calls === 2) return { operations: [snapshot], next_cursor: 'next' };
    assert.equal(options.cursor, 'next'); return { operations: [snapshot, { ...snapshot, id: other }] };
  } }));
  const old = feature.history(); await feature.history();
  delayed.resolve({ operations: [] }); await old;
  assert.equal(feature.rows.length, 1);
  await feature.history({ more: true }); assert.equal(feature.rows.length, 2);
  feature.close(); await assert.rejects(feature.history(), /Session closed/);
});

test('result access and delivery updates preserve successful business work', async () => {
  const updates: OperationSessionUpdate<Result>[] = [];
  const feature = session(client({ async *subscribe() {
    yield { snapshot: { ...snapshot, completion_delivery: { state: 'delivered', attempts: 3 } } };
    yield { snapshot: { ...snapshot, state: 'running', latest_sequence: 3 } };
  } }), update => { updates.push(update); });
  await feature.open(id); await feature.watching;
  assert.equal(feature.snapshot?.state, 'succeeded');
  assert.equal(feature.snapshot?.completion_delivery.state, 'delivered');
  assert.equal(updates.length, 2);
  assert.equal((await feature.result()).result?.csv, snapshot.result?.csv);
  feature.close();
});

test('downloads require confirmed success and fence a delayed blob after selection changes', async () => {
  const artifact = { id: other, name: 'export.csv', uri: 'private', size_bytes: 3, sha256: 'a'.repeat(64) };
  const blob = deferred<Blob>(); let downloading = false;
  const feature = session(client({ get: async selected => ({ ...snapshot, id: selected, artifacts: [artifact] }), download: async () => {
    downloading = true; return { blob: () => blob.promise } as Response;
  } }));
  await feature.open(id); const pending = feature.download();
  while (!downloading) await Promise.resolve();
  await feature.open(other); blob.resolve(new Blob(['csv']));
  await assert.rejects(pending, /Session changed/); feature.close();

  let downloads = 0;
  const uncertain = session(client({ get: async () => ({ ...snapshot, state: 'requires_reconciliation', artifacts: [artifact] }), download: async () => { downloads++; return new Response('csv'); } }));
  await uncertain.open(id); await assert.rejects(uncertain.download(), /not confirmed complete/);
  assert.equal(downloads, 0); uncertain.close();
});

test('real browser client can supply a typed feature session', async () => {
  const api = new GregaleOperationClient({ apiURL: 'https://api.example.test', credential: () => 'tenant', fetch: async () => Response.json(snapshot) });
  const feature = new GregaleOperationSession<Result>({ client: api, appID: id, scope: 'default', definitionID: other });
  await feature.open(id); assert.equal((await feature.result()).result?.csv, snapshot.result?.csv); feature.close();
});
