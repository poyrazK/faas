import assert from 'node:assert/strict';
import test from 'node:test';
import { DevService, OpenAPI } from '../src/generated/index.js';

test('suspend/resume enrollment, requests and original-owner receipts are typed', async () => {
  const previousFetch = globalThis.fetch;
  const previousBase = OpenAPI.BASE;
  const plan = { version: 1 as const, checkpoint: 'after_setup' as const, workloads: ['worker'], pause_ms: 1000, within_ms: 2000 };
  const proof = {
    version: 1, run_id: 'a'.repeat(32), spec: plan, plan_sha256: 'b'.repeat(64), status: 'complete',
    receipts: [{ id: '11111111-1111-4111-8111-111111111111', account_id: '22222222-2222-4222-8222-222222222222', app_id: '33333333-3333-4333-8333-333333333333', run_id: 'a'.repeat(32), workload: 'worker', deployment_id: '44444444-4444-4444-8444-444444444444', instance_id: '55555555-5555-4555-8555-555555555555', recorder_session_id: '66666666-6666-4666-8666-666666666666', state: 'complete', method: 'in_place', pause_ms: 1000, resume_ms: 120 }],
  };
  try {
    OpenAPI.BASE = 'https://rehearsal.example.test';
    globalThis.fetch = async (_input, init) => {
      if (init?.method === 'PUT') {
        assert.deepEqual(JSON.parse(String(init.body)).suspend_resume, plan);
        return new Response(JSON.stringify({ webhook_mode: 'capture', policy_version: 1, suspend_resume_sha256: proof.plan_sha256 }), { status: 200, headers: { 'content-type': 'application/json' } });
      }
      return new Response(JSON.stringify(proof), { status: 200, headers: { 'content-type': 'application/json' } });
    };
    const ack = await DevService.registerScenarioTest({ runId: proof.run_id, requestBody: { members: [{ workload: 'worker', app_slug: 'isolated-worker' }], suspend_resume: plan } });
    assert.equal(ack.suspend_resume_sha256, proof.plan_sha256);
    const started = await DevService.beginScenarioSuspendResume({ runId: proof.run_id });
    const result = await DevService.getScenarioSuspendResume({ runId: proof.run_id });
    assert.equal(started.spec.checkpoint, 'after_setup');
    assert.equal(result.receipts[0]?.recorder_session_id, proof.receipts[0]!.recorder_session_id);
    assert.equal(result.receipts[0]?.method, 'in_place');
    assert.equal(result.receipts[0]?.resume_ms, 120);
  } finally {
    globalThis.fetch = previousFetch;
    OpenAPI.BASE = previousBase;
  }
});

test('running-job checkpoint retains the original dispatch and simulation identity', async () => {
  const previousFetch = globalThis.fetch;
  const previousBase = OpenAPI.BASE;
  const id = '11111111-1111-4111-8111-111111111111';
  const proof = {
    version: 1, run_id: 'a'.repeat(32),
    spec: { version: 1, checkpoint: 'managed_job', workloads: ['worker'], worker_checkpoint: 'after-commit', pause_ms: 10, within_ms: 1000 },
    plan_sha256: 'b'.repeat(64), status: 'complete',
    receipts: [{ id, account_id: id, app_id: id, run_id: 'a'.repeat(32), workload: 'worker', deployment_id: id, instance_id: id, recorder_session_id: id, state: 'complete', method: 'in_place', pause_ms: 10, resume_ms: 20, invocation_id: id, invocation_attempt: 1, checkpoint_attempt_id: id }],
  };
  try {
    OpenAPI.BASE = 'https://rehearsal.example.test';
    globalThis.fetch = async () => new Response(JSON.stringify(proof), { status: 200, headers: { 'content-type': 'application/json' } });
    const result = await DevService.getScenarioSuspendResume({ runId: proof.run_id });
    assert.equal(result.spec.checkpoint, 'managed_job');
    assert.equal(result.spec.worker_checkpoint, 'after-commit');
    assert.equal(result.receipts[0]?.invocation_id, id);
    assert.equal(result.receipts[0]?.invocation_attempt, 1);
    assert.equal(result.receipts[0]?.checkpoint_attempt_id, id);
  } finally {
    globalThis.fetch = previousFetch;
    OpenAPI.BASE = previousBase;
  }
});
