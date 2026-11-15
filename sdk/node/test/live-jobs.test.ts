import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createLiveJob, parseLiveJobSnapshot, type LiveJobSink, type LiveJobSource, type LiveJobSnapshot } from '../src/live-jobs.js';
import { createLiveJobBridge } from '../src/live-job-bridge.js';
import { realtimeLiveJobSource } from '../src/live-job-realtime.js';
import { REALTIME_RESUME_SUBPROTOCOL, type RealtimeSocket } from '../src/realtime-resume.js';
const fixture = JSON.parse(readFileSync(new URL('../../../request-contracts/live-jobs.json', import.meta.url), 'utf8')) as { resource_id: string; cases: { name: string; snapshot: unknown; valid: boolean }[] };
const base = fixture.cases[0]!.snapshot as LiveJobSnapshot;
const complete = { ...base, status: 'succeeded', succeeded: 3, running: 0, report: 'ready' };
const event = { version: 1, resource_id: 'import-1', kind: 'progress' };
const delay = (ms = 0) => new Promise<void>(resolve => setTimeout(resolve, ms));
async function until(check: () => boolean): Promise<void> { for (let i = 0; i < 1000; i++) { if (check()) return; await delay(1); } assert.fail('live view timed out'); }
function source() {
  let sink!: LiveJobSink, starts = 0, stops = 0;
  const subscribe: LiveJobSource = next => { sink = next; starts++; queueMicrotask(() => sink.ready()); return () => { stops++; }; };
  return { subscribe, get sink() { return sink; }, get starts() { return starts; }, get stops() { return stops; } };
}
for (const item of fixture.cases) test('shared snapshot: ' + item.name, () => {
  if (!item.valid) { assert.throws(() => parseLiveJobSnapshot(item.snapshot, fixture.resource_id)); return; }
  const value = parseLiveJobSnapshot(item.snapshot, fixture.resource_id);
  assert.ok(Object.isFrozen(value)); assert.ok(Object.isFrozen(value.failures));
  assert.equal(JSON.stringify(value).includes('secret-discard-me'), false);
  assert.equal(JSON.stringify(value.failures).includes('discard'), false);
});
test('lazy shared listeners, duplicate/out-of-order hints, authoritative completion and report verification', async () => {
  const transport = source(); let value: unknown = base, reads = 0;
  const live = createLiveJob({ resourceId: 'import-1', timeoutMs: 2000, source: transport.subscribe, loadSnapshot: async () => { reads++; return value; } });
  assert.equal(transport.starts, 0);
  const off = live.subscribe(() => {}), off2 = live.subscribe(() => {});
  await until(() => live.getView().snapshot?.status === 'running'); assert.equal(transport.starts, 1);
  for (let i = 0; i < 500; i++) transport.sink.changed({ ...event, kind: 'report_ready', status: 'succeeded', report: 'ready' });
  await until(() => reads >= 2); await delay(); assert.equal(live.getView().snapshot?.status, 'running'); assert.ok(reads <= 3);
  const before = reads; transport.sink.changed({ ...event, resource_id: 'other' }); await delay(); assert.equal(reads, before);
  value = { ...complete, report: 'pending' }; transport.sink.changed(event);
  await until(() => live.getView().snapshot?.status === 'succeeded'); assert.equal(live.getView().connection, 'live');
  transport.sink.disconnected(); assert.equal(live.getView().connection, 'reconnecting');
  value = complete; transport.sink.ready(); await until(() => live.getView().connection === 'complete');
  assert.equal(live.getView().snapshot?.report, 'ready'); assert.equal(transport.stops, 1); off(); off2();
});
test('authorization loss clears state and fences cancellation-ignoring snapshots', async () => {
  const transport = source(); let release!: (value: unknown) => void;
  const live = createLiveJob({ resourceId: 'import-1', timeoutMs: 2000, source: transport.subscribe, loadSnapshot: () => new Promise(resolve => { release = resolve; }) });
  const off = live.subscribe(() => {}); await until(() => !!release);
  transport.sink.failed(); release(complete); await delay();
  assert.deepEqual(live.getView(), { connection: 'error', error: 'subscription_failed' }); assert.equal(transport.stops, 1); off();
});
test('an in-flight submission intent can become an accepted run without resubmission', async () => {
  const transport = source(); let value: unknown = { ...base, run_id: null, status: 'submission_unresolved', total: 0, succeeded: 0, running: 0 };
  const live = createLiveJob({ resourceId: 'import-1', timeoutMs: 1000, source: transport.subscribe, loadSnapshot: async () => value });
  const iterator = live.updates(); assert.equal((await iterator.next()).value?.status, 'submission_unresolved');
  value = complete; transport.sink.changed(event); assert.equal((await iterator.next()).value?.report, 'ready');
  assert.equal((await iterator.next()).done, true); assert.equal(transport.stops, 1);
});
test('deadlines, cancellation, limits and early iterator return release the source', async () => {
  for (const mode of ['deadline', 'cancel', 'limit', 'return']) {
    const transport = source(), abort = new AbortController(); let reads = 0;
    const live = createLiveJob({ resourceId: 'import-1', timeoutMs: mode === 'deadline' ? 15 : 2000, signal: abort.signal, maxSnapshots: 1, source: transport.subscribe,
      loadSnapshot: () => { reads++; return mode === 'deadline' ? new Promise(() => {}) : Promise.resolve(base); } });
    const iterator = live.updates();
    if (mode === 'deadline') await assert.rejects(iterator.next(), { code: 'deadline_exceeded' });
    else {
      assert.equal((await iterator.next()).value?.status, 'running');
      if (mode === 'return') await iterator.return!(undefined);
      else { if (mode === 'cancel') abort.abort(); else transport.sink.changed(event); await assert.rejects(iterator.next(), { code: mode === 'cancel' ? 'cancelled' : 'snapshot_limit' }); }
    }
    assert.equal(transport.stops, 1); assert.equal(reads, 1);
  }
});
test('replacing identity clears the old view; late results and wrong run cannot leak', async () => {
  const a = source(), b = source();
  const first = createLiveJob({ resourceId: 'import-1', timeoutMs: 1000, source: a.subscribe, loadSnapshot: async () => base });
  const off = first.subscribe(() => {}); await until(() => !!first.getView().snapshot); first.close(); off();
  const second = createLiveJob({ resourceId: 'import-1', timeoutMs: 1000, source: b.subscribe, loadSnapshot: async () => { throw new Error('secret denial'); } });
  const off2 = second.subscribe(() => {}); assert.equal(second.getView().snapshot, undefined);
  await until(() => second.getView().connection === 'error'); assert.equal(first.getView().snapshot, undefined); assert.equal(second.getView().snapshot, undefined); off2();
});
test('bridge shares reads, publishes only changes, verifies terminal snapshot and ends leases', async () => {
  let reads = 0, value: unknown = base; const published: string[] = [];
  const bridge = createLiveJobBridge({ pollDelayMs: 5, leaseMs: 100, requestTimeoutMs: 100, loadSnapshot: async () => { reads++; await delay(2); return value; },
    publish: async (_snapshot, event) => { published.push(event.kind); } });
  bridge.watch('import-1'); bridge.watch('import-1');
  const [left, right] = await Promise.all([bridge.snapshot('import-1'), bridge.snapshot('import-1')]); assert.equal(left, right);
  await until(() => published.length === 1); await delay(15); assert.equal(published.length, 1); assert.ok(reads < 10);
  value = complete; await until(() => published.includes('report_ready')); const count = reads; await delay(20); assert.equal(reads, count); bridge.close();
  let attempts = 0;
  const expiring = createLiveJobBridge({ pollDelayMs: 2, leaseMs: 15, requestTimeoutMs: 100, loadSnapshot: async () => { attempts++; return base; }, publish: async () => {} });
  expiring.watch('import-1'); await delay(40); const expired = attempts; await delay(20); assert.equal(attempts, expired); expiring.close();
});
class Socket extends EventTarget implements RealtimeSocket {
  protocol = REALTIME_RESUME_SUBPROTOCOL; readyState = 0; sent: Record<string, unknown>[] = [];
  send(value: string) { this.sent.push(JSON.parse(value) as Record<string, unknown>); }
  receive(value: unknown) { this.dispatchEvent(new MessageEvent('message', { data: JSON.stringify(value) })); }
  close() { this.readyState = 3; this.dispatchEvent(new Event('close')); }
  open() { this.readyState = 1; this.dispatchEvent(new Event('open')); }
}
test('native transport reconnects, recovers history gaps and fails closed on revocation', async () => {
  const sockets: Socket[] = []; let reads = 0;
  const live = createLiveJob({ resourceId: 'import-1', timeoutMs: 3000, loadSnapshot: async () => { reads++; return base; },
    source: realtimeLiveJobSource({ url: 'wss://app.test/realtime', channel: 'import:import-1', retryInitialMs: 0, retryMaxMs: 0,
      webSocketFactory: () => { const socket = new Socket(); sockets.push(socket); queueMicrotask(() => socket.open()); return socket; } }) });
  const off = live.subscribe(() => {});
  await until(() => sockets[0]?.sent.length === 1); sockets[0]!.receive({ type: 'subscribed', channel: 'import:import-1', sequence: 0 }); await until(() => reads === 1);
  sockets[0]!.close(); await until(() => sockets.length === 2 && sockets[1]!.sent.length === 1);
  sockets[1]!.receive({ type: 'resync_required', channel: 'import:import-1', oldest_sequence: 10, latest_sequence: 12 });
  await until(() => sockets[2]?.sent.length === 1);
  sockets[2]!.receive({ type: 'subscribed', channel: 'import:import-1', sequence: 12 }); await until(() => reads >= 2);
  sockets[2]!.receive({ type: 'error', channel: 'import:import-1', code: 'access_revoked' });
  await until(() => live.getView().connection === 'error'); assert.equal(live.getView().snapshot, undefined); off();
});
