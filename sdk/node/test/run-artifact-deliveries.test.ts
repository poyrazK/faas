import test from 'node:test';
import assert from 'node:assert/strict';
import { FaaSClient } from '../src/preview.js';

test('artifact delivery keeps its storage identity and reads app-owned receipts', async () => {
  const seen: Array<{ path: string; method: string; body: unknown }> = [];
  const client = new FaaSClient('https://api.example.test', { fetch: async (input, init) => {
    const url = new URL(input.toString());
    seen.push({ path: url.pathname, method: String(init?.method), body: init?.body ? JSON.parse(String(init.body)) : undefined });
    return new Response(JSON.stringify(url.pathname.endsWith('/download-url')
      ? { url: 'https://objects.example.test/report', method: 'GET', headers: {}, expires_at: '2026-11-17T16:00:00Z' }
      : { id: 'delivery', status: 'pending', size_bytes: 20, content_type: 'text/html', receipt_url: '/receipt', created_at: '2026-11-17T15:00:00Z' }),
      { status: url.pathname.endsWith('/reports') ? 202 : 200, headers: { 'content-type': 'application/json' } });
  } });
  try {
    const body = { idempotency_key: 'completion-one', execution_id: 'run', artifact_name: 'report.html', size_bytes: 20, sha256: 'sha256:expected', content_type: 'text/html' };
    const first = await client.deliverRunArtifact('app', 'reports', body);
    const replay = await client.deliverRunArtifact('app', 'reports', body);
    assert.equal(first.status, 'pending'); assert.equal(first.id, replay.id);
    assert.deepEqual(seen[0], seen[1]);
    await client.getRunArtifactDelivery('app', 'reports', 'delivery');
    const download = await client.signRunArtifactDelivery('app', 'reports', 'delivery');
    assert.equal(download.method, 'GET');
    assert.deepEqual(seen.map(r => r.path), ['/v1/apps/app/run-artifact-deliveries/reports', '/v1/apps/app/run-artifact-deliveries/reports', '/v1/apps/app/run-artifact-deliveries/reports/delivery', '/v1/apps/app/run-artifact-deliveries/reports/delivery/download-url']);
  } finally { client.uninstall(); }
});
