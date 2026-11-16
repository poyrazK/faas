import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { setImmediate as nextTurn } from 'node:timers/promises';
import { createClient, ErrCapacity, ErrNotFound, type ResponseInfo } from '../src/index.js';

interface Case {
  name: string; method?: 'POST'; phase?: 'headers' | 'network' | 'body'; timeout_ms?: number; max_attempts?: number;
  responses?: { status: number; request_id?: string; retry_after?: string }[];
  outcome: 'success' | 'capacity' | 'not_found' | 'network' | 'timeout' | 'cancelled';
  calls: number; statuses: number[]; attempts: number[]; ids: (string | null)[]; stable_key?: boolean; closed?: number;
}
const contract = JSON.parse(readFileSync(new URL('../../../request-contracts/async-options.json', import.meta.url), 'utf8')) as { version: number; cases: Case[] };

for (const fixture of contract.cases) {
  test(`shared Node/Python request contract: ${fixture.name}`, { timeout: 5000 }, async t => {
    assert.equal(contract.version, 1);
    if (fixture.phase === 'body') t.mock.method(console, 'error', () => {});
    const reports: ResponseInfo[] = [], keys: (string | null)[] = [];
    const controller = new AbortController(), reason = new Error('caller reason'), network = new TypeError('network failed');
    let calls = 0, closed = 0;
    let started!: () => void;
    const entered = new Promise<void>(resolve => { started = resolve; });
    const client = createClient('https://contracts.test', { fetch: async (_url, init) => {
      calls++; keys.push(new Headers(init?.headers).get('Idempotency-Key')); started();
      if (fixture.phase === 'headers') return new Promise<Response>(() => {});
      if (fixture.phase === 'network') throw network;
      const response = fixture.responses![Math.min(calls - 1, fixture.responses!.length - 1)]!;
      const headers: Record<string, string> = { 'Content-Type': 'application/json' };
      if (response.request_id) headers['X-Request-ID'] = response.request_id;
      if (response.retry_after) headers['Retry-After'] = response.retry_after;
      if (fixture.phase === 'body') return new Response(new ReadableStream({
        start(stream) { stream.enqueue(new TextEncoder().encode('{"id":')); }, cancel() { closed++; },
      }), { status: 200, headers });
      return Response.json(response.status < 400 ? { id: fixture.name } : {
        code: response.status === 404 ? 'not_found' : 'capacity_unavailable', tx_id: 'original-tx', status: response.status,
      }, { status: response.status, headers });
    } });
    const options = {
      signal: controller.signal, timeoutMs: fixture.timeout_ms ?? 2000,
      retry: fixture.max_attempts && fixture.max_attempts > 1 ? { maxAttempts: fixture.max_attempts, backoffMs: 1 } : false as const,
      onResponse: (info: ResponseInfo) => { reports.push(info); },
    };
    const pending = fixture.method === 'POST' ? client.apps.createApp({ requestBody: { slug: 'case' } }, options)
      : client.apps.getApp({ slug: 'case' }, options);
    if (fixture.outcome === 'cancelled') {
      const rejected = assert.rejects(pending, error => error === reason);
      await entered; controller.abort(reason); await rejected;
    } else if (fixture.outcome === 'timeout') await assert.rejects(pending, { name: 'TimeoutError' });
    else if (fixture.outcome === 'network') await assert.rejects(pending, error => error === network);
    else if (fixture.outcome === 'capacity' || fixture.outcome === 'not_found') await assert.rejects(pending, error =>
      error instanceof (fixture.outcome === 'capacity' ? ErrCapacity : ErrNotFound) && error.txId === 'original-tx');
    else assert.equal((await pending).id, fixture.name);
    await nextTurn();
    assert.equal(calls, fixture.calls);
    assert.deepEqual(reports.map(info => info.status), fixture.statuses);
    assert.deepEqual(reports.map(info => info.attempt), fixture.attempts);
    assert.deepEqual(reports.map(info => info.requestId), fixture.ids);
    for (const info of reports) {
      assert.ok(Object.isFrozen(info));
      assert.deepEqual(Object.keys(info).sort(), ['attempt', 'durationMs', 'requestId', 'status']);
      assert.ok(Number.isFinite(info.durationMs) && info.durationMs >= 0);
    }
    if (fixture.stable_key) { assert.equal(new Set(keys).size, 1); assert.ok(keys[0]); }
    if (fixture.closed !== undefined) assert.equal(closed, fixture.closed);
  });
}
