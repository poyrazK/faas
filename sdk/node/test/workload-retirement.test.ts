import test from 'node:test';
import assert from 'node:assert/strict';
import { FaaSClient, ProjectsService } from '../src/index.js';
import type { EnvironmentDefinition, EnvironmentWorkloadRetirement } from '../src/index.js';

test('GitOps review transport preserves workload disposition and original recovery identity', async () => {
  const original = '11111111-2222-4333-8444-555555555555';
  const definition: EnvironmentDefinition = {
    api_version: 'gregale.dev/environment/v1', project: 'shop', environment: 'staging',
    workload_pruning_policy: 'retain', workload_recoveries: { worker: original },
    workloads: { worker: { app: 'orders-worker' } },
  };
  const client = new FaaSClient('https://api.example.test', { token: 'token', fetch: async (input, init) => {
    const url = input instanceof Request ? input.url : String(input);
    assert.equal(url, 'https://api.example.test/v1/projects/shop/environments/staging/gitops/revisions/preview');
    assert.equal(init?.method, 'POST');
    return Response.json({ definition, commit_sha: 'a'.repeat(40), definition_digest: 'b'.repeat(64), generation: 4 });
  } });
  try {
    const reviewed = await ProjectsService.previewEnvironmentGitRevision({ slug: 'shop', environment: 'staging', requestBody: { commit_sha: 'a'.repeat(40) } });
    assert.deepEqual(reviewed.definition, definition);
  } finally { client.uninstall(); }
});

test('GitOps status keeps retirement identity and new recovery authority separate', async () => {
  const original = '11111111-2222-4333-8444-555555555555';
  const retirement: EnvironmentWorkloadRetirement = {
    id: original, source_id: original, environment_id: original, app_id: original,
    resource: 'workload/worker', revision_id: original, generation: 3, phase: 'recovering',
    requested_at: '2026-10-15T10:00:00Z', recovery_revision_id: original, recovery_generation: 4,
  };
  const client = new FaaSClient('https://api.example.test', { token: 'token', fetch: async (input) => {
    const url = input instanceof Request ? input.url : String(input);
    assert.equal(url, 'https://api.example.test/v1/projects/shop/environments/staging/gitops');
    return Response.json({ workload_retirements: [retirement], runs: [] });
  } });
  try {
    const status = await ProjectsService.getEnvironmentGitOps({ slug: 'shop', environment: 'staging' });
    assert.deepEqual(status.workload_retirements, [retirement]);
  } finally { client.uninstall(); }
});
