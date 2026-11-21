import test from 'node:test';
import assert from 'node:assert/strict';
import { FaaSClient } from '../src/preview.js';

test('environment diagnostic watch resumes a durable cursor and reports retention gaps', async () => {
  const calls: URL[] = [];
  const client = new FaaSClient('https://api.example.test', { fetch: async (input) => {
    const url = new URL(input.toString()); calls.push(url);
    return new Response(JSON.stringify(calls.length === 1 ? {
      events: [{ sequence: 8, stage: 'build', code: 'build_output', message: 'installing', attempt: 1, created_at: '2026-11-15T00:00:00Z' }],
      gap: true, next_sequence: 8, status: 'building',
    } : { events: [], next_sequence: 8, status: 'ready' }), { status: 200, headers: { 'content-type': 'application/json' } });
  } });
  try {
    const events = [];
    for await (const e of client.watchExecutionEnvironment('env/1', { pollIntervalMs: 1 })) events.push(e);
    assert.deepEqual(events.map(e => e.type), ['gap', 'diagnostic', 'terminal']);
    assert.equal(calls[0]?.pathname, '/v1/execution-environments/env%2F1/events');
    assert.equal(calls[1]?.searchParams.get('after'), '8');
  } finally { client.uninstall(); }
});

test('environment watches abort without resubmitting or cancelling the build', async () => {
  const controller = new AbortController(); let calls = 0;
  const client = new FaaSClient('https://api.example.test', { fetch: async (_input, init) => {
    calls++; assert.equal(init?.method ?? 'GET', 'GET');
    return new Response(JSON.stringify({ events: [], next_sequence: 0, status: 'building' }), { status: 200, headers: { 'content-type': 'application/json' } });
  } });
  try {
    const iterator = client.watchExecutionEnvironment('env', { signal: controller.signal, pollIntervalMs: 10000 });
    const pending = iterator.next(); setTimeout(() => controller.abort(new Error('stop watcher')), 20);
    await assert.rejects(pending, /stop watcher/); assert.equal(calls, 1);
  } finally { client.uninstall(); }
});
