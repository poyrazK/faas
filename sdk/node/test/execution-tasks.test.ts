import test from 'node:test';
import assert from 'node:assert/strict';
import { FaaSClient } from '../src/preview.js';

test('named task invocation preserves explicit retry identity and task lineage', async () => {
  const seen: Array<{ path: string; key: string | null; body: unknown }> = [];
  const client = new FaaSClient('https://api.example.test', { fetch: async (input, init) => {
    const url = new URL(input.toString());
    seen.push({ path: url.pathname, key: new Headers(init?.headers).get('Idempotency-Key'), body: JSON.parse(String(init?.body)) });
    return new Response(JSON.stringify({ id: 'run-1', task: { id: 'version-1', name: 'report', version: 1, digest: 'digest' }, status: 'queued' }), { status: 202, headers: { 'content-type': 'application/json' } });
  } });
  try {
    const request = { name: 'report', version: 1, input: { rows: 3 } };
    const first = await client.invokeExecutionTask(request, 'report-call-123');
    const replay = await client.invokeExecutionTask(request, 'report-call-123');
    assert.deepEqual(seen[0], seen[1]);
    assert.equal(seen[0]?.path, '/v1/run-tasks/invoke');
    assert.equal(seen[0]?.key, 'report-call-123');
    assert.deepEqual(seen[0]?.body, request);
    assert.equal(first.task?.name, 'report');
    assert.equal(replay.id, first.id);
  } finally { client.uninstall(); }
});
