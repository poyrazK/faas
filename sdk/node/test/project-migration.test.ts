import assert from 'node:assert/strict';
import test from 'node:test';
import { ProjectsService, OpenAPI } from '../src/index.js';

test('migration applies the reviewed source and request-only initial configuration', async () => {
  const originalFetch = globalThis.fetch;
  const originalBase = OpenAPI.BASE;
  OpenAPI.BASE = 'https://api.invalid';
  const requestBody = { plan_token: 'reviewed', only: ['api'], initial_secrets: { api: { DATABASE_URL: 'request-only' } } };
  try {
    let calls = 0;
    globalThis.fetch = async (input, init) => {
      calls++;
      assert.equal(new URL(String(input)).pathname, '/v1/projects/apply-plan');
      assert.equal(init?.method, 'POST');
      assert.deepEqual(JSON.parse(String(init?.body)), requestBody);
      return new Response(JSON.stringify({ project_id: 'project' }), { headers: { 'Content-Type': 'application/json' } });
    };
    const response = await ProjectsService.applyCachedProjectPlan({ requestBody });
    assert.equal(response.project_id, 'project');
    assert.equal(calls, 1);
  } finally {
    globalThis.fetch = originalFetch;
    OpenAPI.BASE = originalBase;
  }
});

test('project migration preserves its identity and scopes resume to its journal', async () => {
  const originalFetch = globalThis.fetch;
  const originalBase = OpenAPI.BASE;
  OpenAPI.BASE = 'https://api.invalid';
  const id = '7a7ab9c4-2995-48d5-85c5-c094ce55c599';
  const calls: { path: string; method: string; body?: unknown }[] = [];
  try {
    globalThis.fetch = async (input, init) => {
      calls.push({ path: new URL(String(input)).pathname, method: init?.method ?? 'GET', body: init?.body ? JSON.parse(String(init.body)) : undefined });
      return new Response(JSON.stringify({ id, project: 'shop', services: [] }), { headers: { 'Content-Type': 'application/json' } });
    };
    await ProjectsService.createProjectMigration({ requestBody: { id, plan_token: 'reviewed', only: ['api', 'web'], initial_secrets: { api: { DATABASE_URL: 'request-only' } }, service_connections: { web: { API_URL: 'api' } }, build_dependencies: { web: ['api'] }, public_service_connections: { web: { VITE_API_URL: 'api' } }, public_build_urls: { web: { NEXT_PUBLIC_AUTH_URL: 'https://auth.example.com/v1' } } } });
    await ProjectsService.getProjectMigration({ migration: id });
    await ProjectsService.resumeProjectMigration({ migration: id, requestBody: { plan_token: 'same-source', expected_version: 4, only: ['api'] } });
    assert.deepEqual(calls.map(c => [c.method, c.path]), [['POST', '/v1/project-migrations'], ['GET', `/v1/project-migrations/${id}`], ['POST', `/v1/project-migrations/${id}/resume`]]);
    assert.deepEqual(calls[2]?.body, { plan_token: 'same-source', expected_version: 4, only: ['api'] });
    assert.deepEqual((calls[0]?.body as { service_connections: unknown }).service_connections, { web: { API_URL: 'api' } });
    assert.deepEqual((calls[0]?.body as { public_service_connections: unknown }).public_service_connections, { web: { VITE_API_URL: 'api' } });
    assert.deepEqual((calls[0]?.body as { public_build_urls: unknown }).public_build_urls, { web: { NEXT_PUBLIC_AUTH_URL: 'https://auth.example.com/v1' } });
    assert.equal(calls.length, 3);
  } finally { globalThis.fetch = originalFetch; OpenAPI.BASE = originalBase; }
});

test('migration naming review keeps repository names separate from app allocations', async () => {
  const originalFetch = globalThis.fetch;
  const originalBase = OpenAPI.BASE;
  OpenAPI.BASE = 'https://api.invalid';
  const requestBody = { repo: 'team/shop', ref: 'main', project_slug: 'shop', migration: true, only: ['api'], app_slugs: { api: 'shop-backend' } };
  try {
    globalThis.fetch = async (input, init) => {
      assert.equal(new URL(String(input)).pathname, '/v1/projects/scan/source-ref');
      assert.deepEqual(JSON.parse(String(init?.body)), requestBody);
      return new Response(JSON.stringify({ can_apply: true, workloads: [{ name: 'api', app_slug: 'shop-backend', root_dir: 'api', command: [], ports: [] }], plan_token: 'reviewed-names' }), { headers: { 'Content-Type': 'application/json' } });
    };
    const plan = await ProjectsService.scanProjectSourceRef({ requestBody });
    assert.equal(plan.workloads?.[0]?.name, 'api');
    assert.equal(plan.workloads?.[0]?.app_slug, 'shop-backend');
  } finally { globalThis.fetch = originalFetch; OpenAPI.BASE = originalBase; }
});
