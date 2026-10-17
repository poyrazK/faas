import assert from 'node:assert/strict';
import test from 'node:test';
import { ProjectsService, OpenAPI } from '../src/index.js';
import type { CheckedProjectReleaseManifest } from '../src/index.js';

test('checked graph activation preserves empty CAS and fails closed on an older server', async () => {
  const oldFetch = globalThis.fetch;
  const oldBase = OpenAPI.BASE;
  OpenAPI.BASE = 'https://api.invalid';
  const manifest: CheckedProjectReleaseManifest = {
    project: 'shop', environment: 'production', ttl_seconds: 1800,
    deployments: { api: '11111111-1111-4111-8111-111111111111' },
    expected_active_release_id: '', expected_fallback_deployments: {},
    expected_policy_revisions: { api: 1 }, service_targets: { api: {} },
  };
  try {
    let calls = 0;
    globalThis.fetch = async (input, init) => {
      calls++;
      assert.equal(new URL(String(input)).pathname, '/v1/projects/shop/environments/production/release-sets/activate');
      assert.equal(init?.method, 'POST');
      assert.deepEqual(JSON.parse(String(init?.body)), manifest);
      return new Response(JSON.stringify({ type: 'about:blank', title: 'Not found', status: 404, code: 'not_found' }), { status: 404, headers: { 'Content-Type': 'application/json' } });
    };
    await assert.rejects(ProjectsService.activateCheckedProjectRelease({ slug: 'shop', environment: 'production', requestBody: manifest }), (error: { status?: number }) => error.status === 404);
    assert.equal(calls, 1);
  } finally {
    globalThis.fetch = oldFetch;
    OpenAPI.BASE = oldBase;
  }
});

test('checked rollback sends historical and current identities without legacy fallback', async () => {
  const oldFetch = globalThis.fetch;
  const oldBase = OpenAPI.BASE;
  OpenAPI.BASE = 'https://api.invalid';
  const manifest: CheckedProjectReleaseManifest = {
    project: 'shop', environment: 'production', ttl_seconds: 1800,
    rollback_release_id: '11111111-1111-4111-8111-111111111111',
    deployments: { api: '33333333-3333-4333-8333-333333333333' },
    expected_active_release_id: '22222222-2222-4222-8222-222222222222',
    expected_fallback_deployments: {}, expected_policy_revisions: { api: 1 }, service_targets: { api: {} },
  };
  try {
    let calls = 0;
    globalThis.fetch = async (input, init) => {
      calls++;
      assert.equal(new URL(String(input)).pathname, '/v1/projects/shop/environments/production/release-sets/rollback');
      assert.equal(init?.method, 'POST');
      assert.deepEqual(JSON.parse(String(init?.body)), manifest);
      return new Response(JSON.stringify({ status: 404, title: 'Not found', code: 'not_found' }), { status: 404, headers: { 'Content-Type': 'application/problem+json' } });
    };
    await assert.rejects(ProjectsService.rollbackCheckedProjectRelease({ slug: 'shop', environment: 'production', requestBody: manifest }), (error: { status?: number }) => error.status === 404);
    assert.equal(calls, 1);
  } finally { globalThis.fetch = oldFetch; OpenAPI.BASE = oldBase; }
});

test('checked deactivation preserves zero TTL and exact fallback routing without legacy fallback', async () => {
  const oldFetch = globalThis.fetch;
  const oldBase = OpenAPI.BASE;
  OpenAPI.BASE = 'https://api.invalid';
  const manifest: CheckedProjectReleaseManifest = {
    deactivate: true, project: 'shop', environment: 'production', ttl_seconds: 0,
    deployments: { api: '22222222-2222-4222-8222-222222222222' },
    expected_active_release_id: '11111111-1111-4111-8111-111111111111',
    expected_fallback_deployments: { api: '22222222-2222-4222-8222-222222222222' },
    expected_policy_revisions: { api: 1 }, service_targets: { api: {} },
  };
  try {
    let calls = 0;
    globalThis.fetch = async (input, init) => {
      calls++;
      assert.equal(new URL(String(input)).pathname, '/v1/projects/shop/environments/production/release-sets/deactivate');
      assert.equal(init?.method, 'POST');
      assert.deepEqual(JSON.parse(String(init?.body)), manifest);
      return new Response(JSON.stringify({ status: 404, title: 'Not found', code: 'not_found' }), { status: 404, headers: { 'Content-Type': 'application/problem+json' } });
    };
    await assert.rejects(ProjectsService.deactivateCheckedProjectRelease({ slug: 'shop', environment: 'production', requestBody: manifest }), (error: { status?: number }) => error.status === 404);
    assert.equal(calls, 1);
  } finally { globalThis.fetch = oldFetch; OpenAPI.BASE = oldBase; }
});

test('candidate staging preserves the manifest and has explicit status and cancellation routes', async () => {
  const oldFetch = globalThis.fetch;
  const oldBase = OpenAPI.BASE;
  OpenAPI.BASE = 'https://api.invalid';
  const manifest: CheckedProjectReleaseManifest = {
    project: 'shop', environment: 'production', ttl_seconds: 1800,
    deployments: { api: '11111111-1111-4111-8111-111111111111' },
    expected_active_release_id: '', expected_fallback_deployments: {},
    expected_policy_revisions: { api: 1 }, service_targets: { api: {} },
  };
  const calls: string[] = [];
  try {
    globalThis.fetch = async (input, init) => {
      const path = new URL(String(input)).pathname;
      calls.push(`${init?.method} ${path}`);
      if (init?.method === 'POST') assert.deepEqual(JSON.parse(String(init?.body)), { manifest, ttl_seconds: 60 });
      return new Response(JSON.stringify({ id: 'stage-id', project: 'shop', environment: 'production', status: 'waiting', created_at: '2026-10-13T00:00:00Z', expires_at: '2026-10-13T00:01:00Z', members: [] }), { headers: { 'Content-Type': 'application/json' } });
    };
    const staged = await ProjectsService.stageCheckedProjectRelease({ slug: 'shop', environment: 'production', requestBody: { manifest, ttl_seconds: 60 } });
    assert.equal(staged.id, 'stage-id');
    await ProjectsService.getProjectReleaseStage({ slug: 'shop', environment: 'production', stage: staged.id });
    await ProjectsService.cancelProjectReleaseStage({ slug: 'shop', environment: 'production', stage: staged.id });
    assert.deepEqual(calls, [
      'POST /v1/projects/shop/environments/production/release-sets/staging',
      'GET /v1/projects/shop/environments/production/release-sets/staging/stage-id',
      'DELETE /v1/projects/shop/environments/production/release-sets/staging/stage-id',
    ]);
    globalThis.fetch = async () => new Response(JSON.stringify({ status: 404, code: 'not_found' }), { status: 404, headers: { 'Content-Type': 'application/json' } });
    await assert.rejects(ProjectsService.stageCheckedProjectRelease({ slug: 'shop', environment: 'production', requestBody: { manifest, ttl_seconds: 60 } }), (error: { status?: number }) => error.status === 404);
  } finally { globalThis.fetch = oldFetch; OpenAPI.BASE = oldBase; }
});
