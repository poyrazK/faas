import test from 'node:test';
import assert from 'node:assert/strict';
import { FaaSClient, type CreateManagedExecutionWorkflowRequest } from '../src/preview.js';

test('pipeline submission uses v2 and cancellation preserves encoded workflow identity', async () => {
  const seen: Array<{ path: string; key: string | null; body?: unknown }> = [];
  const client = new FaaSClient('https://api.example.test', { fetch: async (input, init) => {
    const url = new URL(input.toString());
    seen.push({ path: url.pathname, key: new Headers(init?.headers).get('Idempotency-Key'), body: init?.body ? JSON.parse(String(init.body)) : undefined });
    return new Response(JSON.stringify({ workflow_id: 'batch:one', plan_id: 'a'.repeat(24), status: 'queued' }), { status: 202, headers: { 'content-type': 'application/json' } });
  } });
  try {
    const body: CreateManagedExecutionWorkflowRequest = { workflow_id: 'batch:one', version: 'v1', batch: { name: 'report', version: 2, items: [{ label: 'one', input: { rows: 3 } }] } };
    await client.createExecutionPipeline(body, 'pipeline-call-123');
    await client.cancelExecutionPipeline('batch:one');
    assert.equal(seen[0]?.path, '/v2/execution-workflows');
    assert.equal(seen[0]?.key, 'pipeline-call-123');
    assert.deepEqual(seen[0]?.body, body);
    assert.equal(seen[1]?.path, '/v1/execution-workflows/batch%3Aone/cancel');
  } finally { client.uninstall(); }
});

test('pipeline watch resumes a retained-tail gap and stops after the terminal page', async () => {
  const { watchExecutionPipeline, RunsService } = await import('../src/preview.js');
  const urls: string[] = [];
  const client = new FaaSClient('https://api.example.test', { fetch: async (input) => {
    const url = new URL(input.toString()); urls.push(url.pathname + url.search);
    const after = Number(url.searchParams.get('after'));
    const page = { pipeline_id: 'pipeline', events: [{ sequence: after === 1 ? 5 : 6, type: 'pipeline', status: after === 1 ? 'running' : 'succeeded', created_at: '2026-11-17T00:00:00Z' }], next_sequence: after === 1 ? 5 : 6, gap: after === 1, terminal: after !== 1 };
    return new Response(JSON.stringify(page), { status: 200, headers: { 'content-type': 'application/json' } });
  } });
  try {
    const events = [];
    for await (const event of watchExecutionPipeline('pipeline', { after: 1, pollIntervalMs: 1 })) events.push(event);
    assert.deepEqual(events.map(e => e.type), ['gap', 'progress', 'progress', 'terminal']);
    assert.equal(urls.length, 2); assert.match(urls[0]!, /after=1/); assert.match(urls[1]!, /after=5/);
    const controller = new AbortController(); controller.abort();
    await assert.rejects(async () => { for await (const ignored of watchExecutionPipeline('pipeline', { signal: controller.signal })) void ignored; });
    assert.equal(urls.length, 2);
    assert.equal(typeof RunsService.rerunExecutionPipeline, 'function');
  } finally { client.uninstall(); }
});
