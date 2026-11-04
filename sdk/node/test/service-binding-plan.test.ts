import assert from 'node:assert/strict';
import test from 'node:test';
import { AppsService, OpenAPI } from '../src/preview.js';
import type { AppBindingInventory, ServiceBindingExplanation, UpdateAppRequest } from '../src/preview.js';
import type { BindingVerification, ManagedServiceProbeSpec } from '../src/preview.js';

test('managed service receipts preserve the versioned frozen context', () => {
  const spec: ManagedServiceProbeSpec = {
    version: 1, caller_deployment_id: '11111111-1111-4111-8111-111111111111',
    scope: 'production', service: 'billing', binding: 'CUSTOM_BILLING_URL',
    target_app_id: '22222222-2222-4222-8222-222222222222',
    target_deployment_id: '33333333-3333-4333-8333-333333333333',
    release_id: '44444444-4444-4444-8444-444444444444', context_revision: 'a'.repeat(64),
  };
  const receipt: BindingVerification = { result: 'passed', source: 'task_guest',
    deployment_id: spec.caller_deployment_id, scope: spec.scope, managed_service: spec };
  assert.deepEqual(JSON.parse(JSON.stringify(receipt)).managed_service, spec);
});

test('service binding preconditions survive the wire and conflicts are never retried unguarded', async () => {
  const oldFetch = globalThis.fetch;
  const oldBase = OpenAPI.BASE;
  OpenAPI.BASE = 'https://api.invalid';
  const fingerprint = 'a'.repeat(64);
  try {
    for (const status of [200, 409]) {
      let calls = 0;
      globalThis.fetch = async (input, init) => {
        calls++;
        assert.equal(new URL(String(input)).pathname, '/v1/apps/demo');
        assert.equal(init?.method, 'PATCH');
        const body = JSON.parse(String(init?.body));
        assert.equal(body.expected_service_binding_fingerprint, fingerprint);
        assert.deepEqual(body.allowed_service_callers, []);
        return new Response(JSON.stringify(status === 200
          ? { id: 'app1', service_binding_fingerprint: 'b'.repeat(64) }
          : { title: 'Service binding plan is stale', status: 409, code: 'conflict' }),
        { status, headers: { 'Content-Type': 'application/json' } });
      };
      const requestBody: UpdateAppRequest = { expected_service_binding_fingerprint: fingerprint, allowed_service_callers: [] };
      const request = AppsService.updateApp({ slug: 'demo', requestBody });
      if (status === 409) {
        await assert.rejects(request, (error: { status?: number }) => error.status === 409);
      } else {
        assert.equal((await request).service_binding_fingerprint, 'b'.repeat(64));
      }
      assert.equal(calls, 1);
    }
  } finally {
    globalThis.fetch = oldFetch;
    OpenAPI.BASE = oldBase;
  }
});

test('managed inventory preserves logical and physical identities without promoting metadata to evidence', async () => {
  const oldFetch = globalThis.fetch;
  const oldBase = OpenAPI.BASE;
  OpenAPI.BASE = 'https://api.invalid';
  const caller = '11111111-1111-4111-8111-111111111111';
  const target = '22222222-2222-4222-8222-222222222222';
  const release = '33333333-3333-4333-8333-333333333333';
  const inventory: AppBindingInventory = {
    app: 'frontend', service_binding_source: 'environment_release', complete: true,
    generated_at: '2026-10-17T10:00:00Z', requested_deployment_id: caller,
    verification_deployment_id: caller, verification_scope: 'production',
    bindings: [{ type: 'service', name: 'billing', binding: 'BILLING_URL', scope: 'production',
      access: 'invoke', state: 'enforced', runtime_status: 'unknown', verification_status: 'unsupported',
      transport: 'https', service_target_deployment_id: target,
      service_context: { source: 'environment_release', caller_deployment_id: caller,
        target_app: 'git-billing', target_app_id: '44444444-4444-4444-8444-444444444444',
        release_id: release, revision: 'a'.repeat(64), routing_status: 'resolved',
        routing_reason: 'active_reviewed_release', verification_supported: false,
        verification_reason: 'managed_service_verification_unsupported' } }],
  };
  try {
    globalThis.fetch = async (input, init) => {
      const url = new URL(String(input));
      assert.equal(init?.method, 'GET');
      assert.equal(url.pathname, '/v1/apps/frontend/bindings');
      assert.equal(url.searchParams.get('deployment_id'), caller);
      return new Response(JSON.stringify(inventory), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
    const got = await AppsService.getAppBindingInventory({ slug: 'frontend', deploymentId: caller });
    assert.deepEqual(got, inventory);
    assert.equal(got.bindings[0]!.service_context!.verification_supported, false);
    assert.equal(got.bindings[0]!.verification, undefined);
  } finally {
    globalThis.fetch = oldFetch;
    OpenAPI.BASE = oldBase;
  }
});

test('deployment service explanations retain selectors, policy source and exact release route', async () => {
  const oldFetch = globalThis.fetch;
  const oldBase = OpenAPI.BASE;
  OpenAPI.BASE = 'https://api.invalid';
  const deployment = '11111111-1111-4111-8111-111111111111';
  const target = '22222222-2222-4222-8222-222222222222';
  const release = '33333333-3333-4333-8333-333333333333';
  const report: ServiceBindingExplanation = {
    caller: 'frontend', target: 'billing', caller_deployment_id: deployment,
    target_app_id: '44444444-4444-4444-8444-444444444444', target_deployment_id: target,
    release_id: release, scope: 'staging', method: 'GET', path: '/invoices',
    result: 'denied', checked_at: '2026-10-17T09:00:00Z', policy_source: 'environment_release',
    canonical_transport: 'https', limitations: 'Metadata observation only.',
    checks: [{ rule: 'inbound_scopes', result: 'denied', reason: 'method_path_not_granted' }],
  };
  let calls = 0;
  try {
    globalThis.fetch = async (input, init) => {
      calls++;
      const url = new URL(String(input));
      assert.equal(init?.method, 'GET');
      assert.equal(url.pathname, '/v1/apps/frontend/bindings/services/billing/explanation');
      assert.equal(url.searchParams.get('deployment_id'), deployment);
      assert.equal(url.searchParams.get('scope'), 'staging');
      assert.equal(url.searchParams.get('method'), 'GET');
      assert.equal(url.searchParams.get('path'), '/invoices');
      return new Response(JSON.stringify(report), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
    assert.deepEqual(await AppsService.explainAppServiceBinding({ slug: 'frontend', service: 'billing', deploymentId: deployment, scope: 'staging', path: '/invoices' }), report);
    assert.equal(calls, 1);
  } finally {
    globalThis.fetch = oldFetch;
    OpenAPI.BASE = oldBase;
  }
});
