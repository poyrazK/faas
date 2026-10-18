import assert from 'node:assert/strict';
import test from 'node:test';
import { DevService, OpenAPI } from '../src/generated/index.js';

test('object fixtures preserve base64 bytes and repeated mutation receipts', async () => {
  const previousFetch = globalThis.fetch;
  const previousBase = OpenAPI.BASE;
  const spec = { version: 1 as const, bucket: 'imports', fixture_sha256: 'a'.repeat(64) };
  const receipt = { version: 1 as const, collector_id: '44444444-4444-4444-8444-444444444444', run_id: 'b'.repeat(32), app_id: '11111111-1111-4111-8111-111111111111', resource_id: '22222222-2222-4222-8222-222222222222', fixture_sha256: spec.fixture_sha256, contract_sha256: 'c'.repeat(64), initial_sha256: 'd'.repeat(64) };
  const object = { key: 'exports/é.bin', size: 3, sha256: 'e'.repeat(64), content_type: 'application/octet-stream', metadata: { owner: 'one' } };
  const evidence = { ...receipt, status: 'complete', provenance: 'platform_object_collector', deployment_id: '33333333-3333-4333-8333-333333333333', spec, before: [], after: [object], mutations: [1, 2].map(sequence => ({ sequence, method: 'PUT', key: object.key, status: 200, object })), cleanup_confirmed: true, collector_closed: true };
  const calls: Array<{ url: string; method?: string; body?: unknown }> = [];
  try {
    OpenAPI.BASE = 'https://rehearsal.example.test';
    globalThis.fetch = async (input, init) => {
      const url = String(input);
      calls.push({ url, method: init?.method, body: init?.body ? JSON.parse(String(init.body)) : undefined });
      if (init?.method === 'DELETE') return new Response(null, { status: 204 });
      return new Response(JSON.stringify(url.endsWith('/seal') ? evidence : receipt), { status: url.endsWith('/seal') ? 200 : 201, headers: { 'content-type': 'application/json' } });
    };
    const fixtures = [{ key: object.key, body: Buffer.from([0, 255, 10]).toString('base64'), content_type: object.content_type, metadata: object.metadata }];
    assert.deepEqual(await DevService.createScenarioObjects({ runId: receipt.run_id, requestBody: { app_slug: 'isolated-api', spec, objects: fixtures } }), receipt);
    assert.deepEqual(await DevService.sealScenarioObjects({ runId: receipt.run_id, requestBody: { deployment_id: evidence.deployment_id } }), evidence);
    await DevService.deleteScenarioObjects({ runId: receipt.run_id });
    assert.deepEqual((calls[0]!.body as { objects: unknown }).objects, fixtures);
    assert.equal(calls[1]!.url, `${OpenAPI.BASE}/v1/dev/test-runs/${receipt.run_id}/objects/seal`);
    assert.equal(calls[2]!.method, 'DELETE');
  } finally {
    globalThis.fetch = previousFetch;
    OpenAPI.BASE = previousBase;
  }
});
