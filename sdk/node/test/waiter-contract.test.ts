import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { setImmediate as nextTurn } from 'node:timers/promises';
import { createClient, WaiterError, ErrNotFound, ErrUnauthorized, type ResponseInfo } from '../src/index.js';
import { createClient as createPreviewClient } from '../src/preview.js';

interface Case {
  name: string; resource: 'build' | 'deployment'; id: string;
  responses: { status: number; body: unknown; retry_after?: string }[];
  timeout_ms?: number; max_polls?: number; max_attempts?: number; stop_on_blocked?: boolean;
  phase?: 'headers' | 'body' | 'network' | 'progress' | 'callback_error';
  outcome?: string; calls: number; polls: number; last?: string | null; progress: string[];
  resume?: boolean; closed?: number; attempts?: number[];
}
const contract = JSON.parse(readFileSync(new URL('../../../request-contracts/waiters.json', import.meta.url), 'utf8')) as { version: number; cases: Case[] };
for (const [surface, factory] of [['public', createClient], ['preview', createPreviewClient]] as const) {
  for (const fixture of contract.cases) {
    test(`${surface} shared waiter: ${fixture.name}`, { timeout: 5000 }, async t => {
      assert.equal(contract.version, 1);
      if (fixture.phase === 'body') t.mock.method(console, 'error', () => {});
      let calls = 0, closed = 0, entered!: () => void;
      const started = new Promise<void>(resolve => { entered = resolve; });
      const reason = new Error('caller stopped'), callbackError = new Error('progress failed'), network = new TypeError('network failed');
      const progress: string[] = [], reports: ResponseInfo[] = [];
      const controller = new AbortController();
      const client = factory('https://contracts.test/prefix', { timeoutMs: 1, fetch: async (url, init) => {
        calls++;
        assert.equal(init!.method, 'GET');
        const path = fixture.resource === 'build' ? `/v1/builds/${encodeURIComponent(fixture.id)}` : `/v1/deployments/${encodeURIComponent(fixture.id)}/timeline`;
        assert.equal(String(url), 'https://contracts.test/prefix' + path);
        if (fixture.phase !== 'progress') entered();
        if (fixture.phase === 'headers') return new Promise<Response>(() => {});
        if (fixture.phase === 'network') throw network;
        if (fixture.phase === 'body') return new Response(new ReadableStream({
          start(stream) { stream.enqueue(new TextEncoder().encode('{"id":')); }, cancel() { closed++; },
        }), { headers: { 'Content-Type': 'application/json' } });
        const response = fixture.responses[calls - 1]!;
        return Response.json(response.body, { status: response.status, headers: response.retry_after ? { 'Retry-After': response.retry_after } : {} });
      } });
      const options = {
        timeoutMs: fixture.timeout_ms ?? 2000, minDelayMs: 1, maxDelayMs: 1, maxPolls: fixture.max_polls,
        retry: fixture.max_attempts ? { maxAttempts: fixture.max_attempts, backoffMs: 1 } : false as const,
        signal: controller.signal, onResponse: (info: ResponseInfo) => { reports.push(info); },
        onProgress: async (value: { status: string; phase?: string }, info: { poll: number; elapsedMs: number }) => {
          progress.push(fixture.resource === 'build' ? value.status : value.phase!);
          assert.ok(Object.isFrozen(value)); assert.ok(Object.isFrozen(info));
          assert.ok(Number.isFinite(info.elapsedMs) && info.elapsedMs >= 0);
          if (fixture.phase === 'callback_error') throw callbackError;
          if (fixture.phase === 'progress') { entered(); await new Promise<void>(() => {}); }
        },
      };
      const wait = () => fixture.resource === 'build' ? client.deployments.waitForBuild({ id: fixture.id }, options)
        : client.deployments.waitForDeployment({ id: fixture.id }, { ...options, stopOnBlocked: fixture.stop_on_blocked });
      const pending = wait();
      if (fixture.outcome === 'caller_cancelled') {
        const rejected = assert.rejects(pending, error => error === reason);
        await started; controller.abort(reason); await rejected;
      } else if (fixture.outcome === 'network' || fixture.outcome === 'callback') {
        await assert.rejects(pending, error => error === (fixture.outcome === 'network' ? network : callbackError));
      } else if (fixture.outcome === 'not_found' || fixture.outcome === 'unauthorized') {
        await assert.rejects(pending, error => error instanceof (fixture.outcome === 'not_found' ? ErrNotFound : ErrUnauthorized) && error.txId === 'original-tx');
      } else if (fixture.outcome) {
        await assert.rejects(pending, error => {
          assert.ok(error instanceof WaiterError);
          assert.equal(error.code, fixture.outcome); assert.equal(error.resource, fixture.resource);
          assert.equal(error.id, fixture.id); assert.equal(error.polls, fixture.polls);
          assert.equal(fixture.resource === 'build' ? error.lastObserved?.status ?? null : error.lastObserved?.phase ?? null, fixture.last);
          return true;
        });
      } else {
        const result = await pending;
        assert.equal(fixture.resource === 'build' ? result.status : 'phase' in result ? result.phase : undefined, fixture.resource === 'build' ? 'succeeded' : 'released');
      }
      if (fixture.resume) assert.equal('phase' in (await wait()), true);
      await nextTurn();
      assert.equal(calls, fixture.calls);
      assert.deepEqual(progress, fixture.progress);
      if (fixture.attempts) assert.deepEqual(reports.map(info => info.attempt), fixture.attempts);
      if (fixture.closed !== undefined) assert.equal(closed, fixture.closed);
    });
  }
}
