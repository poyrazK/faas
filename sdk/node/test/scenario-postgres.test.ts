import assert from 'node:assert/strict';
import test from 'node:test';
import { DevService, OpenAPI } from '../src/generated/index.js';

test('PostgreSQL fixture requests preserve SQL and independent duplicate evidence', async () => {
  const previousFetch = globalThis.fetch;
  const previousBase = OpenAPI.BASE;
  const sql = "CREATE TABLE public.imports(item text);\nINSERT INTO public.imports VALUES ('é');\n";
  const spec = { version: 1 as const, fixture_sha256: 'a'.repeat(64), tables: [{ schema: 'public' as const, name: 'imports', columns: ['item'] }], max_lock_wait_ms: 50 };
  const receipt = { version: 1 as const, run_id: 'b'.repeat(32), app_id: '11111111-1111-4111-8111-111111111111', resource_id: '22222222-2222-4222-8222-222222222222', fixture_sha256: spec.fixture_sha256, contract_sha256: 'c'.repeat(64), initial_sha256: 'd'.repeat(64) };
  const duplicate = [{ item: 'same' }, { item: 'same' }];
  const evidence = { ...receipt, status: 'complete', provenance: 'platform_postgres_collector', deployment_id: '33333333-3333-4333-8333-333333333333', spec, before: [{ table: 'public.imports', rows: [] }], after: [{ table: 'public.imports', rows: duplicate }], changes: [{ table: 'public.imports', inserted: duplicate, deleted: [] }], lock_samples: 3, max_observed_lock_wait_ms: 100, lock_budget_passed: false, cleanup_confirmed: true };
  const calls: Array<{ url: string; body: unknown }> = [];
  try {
    OpenAPI.BASE = 'https://rehearsal.example.test';
    globalThis.fetch = async (input, init) => {
      const url = String(input);
      calls.push({ url, body: JSON.parse(String(init?.body)) });
      return new Response(JSON.stringify(url.endsWith('/seal') ? evidence : receipt), { status: url.endsWith('/seal') ? 200 : 201, headers: { 'content-type': 'application/json' } });
    };
    assert.deepEqual(await DevService.createScenarioPostgres({ runId: receipt.run_id, requestBody: { app_slug: 'isolated-api', spec, sql } }), receipt);
    assert.deepEqual(await DevService.sealScenarioPostgres({ runId: receipt.run_id, requestBody: { deployment_id: evidence.deployment_id } }), evidence);
    assert.equal((calls[0]!.body as { sql: string }).sql, sql);
    assert.equal(calls[1]!.url, `${OpenAPI.BASE}/v1/dev/test-runs/${receipt.run_id}/postgres/seal`);
  } finally {
    globalThis.fetch = previousFetch;
    OpenAPI.BASE = previousBase;
  }
});
