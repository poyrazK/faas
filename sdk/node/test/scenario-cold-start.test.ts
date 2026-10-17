import assert from 'node:assert/strict';
import test from 'node:test';
import { DevService, OpenAPI } from '../src/generated/index.js';

test('cold-start enrollment and host boot receipts retain their typed contract', async () => {
  const previousFetch = globalThis.fetch;
  const previousBase = OpenAPI.BASE;
  const plan = { version: 1 as const, within_ms: 1000 };
  const acknowledgement = { webhook_mode: 'capture', policy_version: 1, cold_start_sha256: 'b'.repeat(64) };
  const proof = {
    version: 1, run_id: 'a'.repeat(32), spec: plan, plan_sha256: acknowledgement.cold_start_sha256,
    status: 'complete', preparations: [{
      id: '11111111-1111-4111-8111-111111111111', account_id: '22222222-2222-4222-8222-222222222222',
      app_id: '33333333-3333-4333-8333-333333333333', run_id: 'a'.repeat(32), workload: 'primary',
      deployment_id: '44444444-4444-4444-8444-444444444444', state: 'booted',
      boot_instance_id: '55555555-5555-4555-8555-555555555555',
      boot_recorder_session_id: '66666666-6666-4666-8666-666666666666',
      wake_id: '77777777-7777-4777-8777-777777777777', method: 'cold_boot', startup_ms: 120,
    }],
  };
  const snapshot = { version: 2, scope: 'managed_webhooks_and_simulated_outbound', run_id: proof.run_id, status: 'complete', workloads: ['primary'], intents: [], captured_count: 0, pending_managed_work: 0, incomplete_managed_effects: 0, suppressed_deliveries: 0, truncated: false, cold_start: proof };
  try {
    OpenAPI.BASE = 'https://rehearsal.example.test';
    globalThis.fetch = async (_input, init) => {
      if (init?.method === 'PUT') {
        assert.deepEqual(JSON.parse(String(init.body)).cold_start, plan);
      }
      return new Response(JSON.stringify(init?.method === 'PUT' ? acknowledgement : snapshot), { status: 200, headers: { 'content-type': 'application/json' } });
    };
    const ack = await DevService.registerScenarioTest({ runId: proof.run_id, requestBody: { members: [{ workload: 'primary', app_slug: 'isolated-api' }], cold_start: plan } });
    assert.equal(ack.cold_start_sha256, proof.plan_sha256);
    const result = await DevService.sealScenarioTestActions({ runId: proof.run_id });
    assert.equal(result.cold_start?.spec.within_ms, 1000);
    assert.equal(result.cold_start?.preparations[0]?.boot_instance_id, proof.preparations[0]!.boot_instance_id);
    assert.equal(result.cold_start?.preparations[0]?.method, 'cold_boot');
  } finally {
    globalThis.fetch = previousFetch;
    OpenAPI.BASE = previousBase;
  }
});
