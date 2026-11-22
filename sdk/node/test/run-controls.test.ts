import test from 'node:test';
import assert from 'node:assert/strict';
import { FaaSClient } from '../src/preview.js';

test('Run controls preserve explicit zero limits, revisions, history filters and cursor', async () => {
  const seen: Array<{ url: URL; method: string; key: string | null; body?: unknown }> = [];
  const client = new FaaSClient('https://api.example.test', { fetch: async (input, init) => {
    const url = new URL(input.toString());
    seen.push({ url, method: init?.method ?? 'GET', key: new Headers(init?.headers).get('Idempotency-Key'), ...(init?.body ? { body: JSON.parse(String(init.body)) } : {}) });
    if (init?.method === 'DELETE') return new Response(null, { status: 204 });
    const response = url.pathname === '/v1/run-history'
      ? { runs: [{ id: 'run-1', attribution: { customer_ref: 'customer-42', agent_ref: 'report-agent' }, status: 'succeeded' }], next_cursor: 'next-page' }
      : { kind: 'customer', ref: 'customer-42', enabled: false, revision: 3, limits: { max_concurrent: 0 }, usage: { active: 1 }, alerts: [] };
    return new Response(JSON.stringify(response), { status: 200, headers: { 'content-type': 'application/json' } });
  } });
  try {
    const request = { expected_revision: 2, enabled: false, limits: { max_concurrent: 0 } };
    const policy = await client.setRunBudget('customer', 'customer-42', request);
    assert.equal(policy.revision, 3);
    assert.deepEqual(seen[0]?.body, request);
    assert.equal(seen[0]?.method, 'PUT');
    assert.equal(seen[0]?.url.pathname, '/v1/run-controls/budgets/customer/customer-42');
    const page = await client.listRunHistory({ customerRef: 'customer-42', agentRef: 'report-agent', taskName: 'report', taskVersion: 1, status: 'succeeded', cursor: 'opaque/page+==', since: '2026-11-16T00:00:00Z' });
    assert.equal(page.runs[0]?.attribution?.customer_ref, 'customer-42');
    assert.equal(page.next_cursor, 'next-page');
    assert.equal(seen[1]?.url.searchParams.get('cursor'), 'opaque/page+==');
    assert.equal(seen[1]?.url.searchParams.get('task_version'), '1');
    assert.equal(seen[1]?.url.searchParams.get('agent_ref'), 'report-agent');
    await client.deleteRunAttributionBinding('api_key', 'public-key-id', 3);
    assert.equal(seen[2]?.url.searchParams.get('expected_revision'), '3');
    assert.equal(seen[1]?.key, null);
  } finally { client.uninstall(); }
});
