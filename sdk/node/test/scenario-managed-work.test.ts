import assert from 'node:assert/strict';
import test from 'node:test';
import { DevService, OpenAPI } from '../src/generated/index.js';

test('managed-worker enrollment and retry evidence survive the HTTP contract', async () => {
  const previousFetch = globalThis.fetch;
  const previousBase = OpenAPI.BASE;
  const plan = { version: 1 as const, primary: 'primary', participants: [{ workload: 'primary', objects: 'read_write' as const }, { workload: 'worker', async: true, objects: 'read' as const }], worker_termination: { workload: 'worker', checkpoint: 'after-commit' } };
  const receipt = { webhook_mode: 'capture', policy_version: 1, command_version: 1, action_evidence_version: 2, managed_work_sha256: 'b'.repeat(64) };
  const app = '33333333-3333-4333-8333-333333333333';
  const deployment = '44444444-4444-4444-8444-444444444444';
  const proof = { version: 1, run_id: 'a'.repeat(32), plan_sha256: receipt.managed_work_sha256, status: 'complete', closing: true, plan, bindings: [{ workload: 'worker', app_id: app, deployment_id: deployment }], jobs: [{ id: '55555555-5555-4555-8555-555555555555', workload: 'worker', app_id: app, deployment_id: deployment, method: 'POST', path: '/import', payload_sha256: 'c'.repeat(64), state: 'completed', attempts: ['retry', 'succeeded'].map((outcome, index) => ({ number: index + 1, instance_id: '66666666-6666-4666-8666-666666666666', deployment_id: deployment, outcome })) }] };
  const snapshot = { version: 2, scope: 'managed_webhooks_and_simulated_outbound', run_id: proof.run_id, status: 'complete', workloads: ['primary', 'worker'], intents: [], captured_count: 0, pending_managed_work: 0, incomplete_managed_effects: 0, suppressed_deliveries: 0, truncated: false, managed_work: proof };
  Object.assign(proof, { worker_fault: { id: '88888888-8888-4888-8888-888888888888', account_id: app, app_id: app, run_id: proof.run_id, workload: 'worker', deployment_id: deployment, instance_id: proof.jobs[0]!.attempts[0]!.instance_id, invocation_id: proof.jobs[0]!.id, checkpoint_attempt_id: '99999999-9999-4999-8999-999999999999', recorder_session_id: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', state: 'terminated', recovery_instance_id: '77777777-7777-4777-8777-777777777777' } });
  proof.jobs[0]!.attempts[1]!.instance_id = '77777777-7777-4777-8777-777777777777';
  const calls: Array<{ method?: string; body?: unknown }> = [];
  try {
    OpenAPI.BASE = 'https://rehearsal.example.test';
    globalThis.fetch = async (_input, init) => {
      calls.push({ method: init?.method, body: init?.body ? JSON.parse(String(init.body)) : undefined });
      return new Response(JSON.stringify(init?.method === 'PUT' ? receipt : snapshot), { status: 200, headers: { 'content-type': 'application/json' } });
    };
    assert.deepEqual(await DevService.registerScenarioTest({ runId: proof.run_id, requestBody: { members: [{ workload: 'primary', app_slug: 'isolated-api' }, { workload: 'worker', app_slug: 'isolated-worker' }], managed_work: plan, command_version: 1 } }), receipt);
    const result = await DevService.sealScenarioTestActions({ runId: proof.run_id });
    assert.deepEqual(result.managed_work, proof);
    assert.equal(result.managed_work?.worker_fault?.recovery_instance_id, '77777777-7777-4777-8777-777777777777');
    assert.equal(result.managed_work?.plan.worker_termination?.checkpoint, 'after-commit');
    assert.deepEqual((calls[0]!.body as { managed_work: unknown }).managed_work, plan);
    assert.equal(calls[1]!.method, 'POST');
  } finally {
    globalThis.fetch = previousFetch;
    OpenAPI.BASE = previousBase;
  }
});
