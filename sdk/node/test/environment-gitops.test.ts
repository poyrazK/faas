import test from 'node:test';
import assert from 'node:assert/strict';
import { FaaSClient, ProjectsService } from '../src/index.js';
import type { EnvironmentGitOpsExplanation } from '../src/index.js';
import type { PreviewEnvironmentGitPromotionResponse } from '../src/index.js';

test('GitOps promotion carries the reviewed hash and requires fresh target qualification', async () => {
  const sha = 'a'.repeat(40), digest = 'b'.repeat(64), hash = 'c'.repeat(64);
  const result: PreviewEnvironmentGitPromotionResponse = {
    project: 'shop', from_environment: 'staging', to_environment: 'production', observed_at: '2026-10-16T12:00:00Z',
    read_only: true, values_redacted: true, requires_target_qualification: true,
    origin: { source_id: 'source', revision_id: 'revision', commit_sha: sha, definition_digest: digest,
      generation: 2, intent_version: 3, graph_id: 'graph', release_set_id: 'release', provider: 'github', repository_id: 123 },
    target: { source_id: 'target', generation: 4, intent_version: 5, commit_sha: sha, definition_digest: digest,
      proposed_digest: digest, manifest_path: 'environments/production.yaml' },
    artifacts: [{ resource: 'workload/api', deployment_id: 'deployment', kind: 'image', image_digest: `sha256:${digest}` }],
    changes: [], blocking_reasons: [], plan_hash: hash,
    target_plan: { manager: 'target', revision: `candidate:${sha}`, commit_sha: sha, generation: 4,
      observed_version: 5, desired_digest: digest, plan_hash: hash, changes: [], blocking_reasons: [] },
  };
  const client = new FaaSClient('https://api.example.test', { token: 'token', fetch: async (input, init) => {
    assert.equal(input instanceof Request ? input.url : String(input), 'https://api.example.test/v1/projects/shop/environments/production/gitops/promotion-plan');
    assert.equal(init?.method, 'POST');
    assert.deepEqual(JSON.parse(String(init?.body)), { from_environment: 'staging', target_commit_sha: sha, expected_plan_hash: hash });
    return Response.json(result);
  } });
  try {
    const preview = await ProjectsService.previewEnvironmentGitPromotion({ slug: 'shop', environment: 'production', requestBody: {
      from_environment: 'staging', target_commit_sha: sha, expected_plan_hash: hash,
    } });
    assert.deepEqual(preview, result);
  } finally { client.uninstall(); }
});

test('GitOps status carries scoped diagnostics without inventing serving', async () => {
  const explanation: EnvironmentGitOpsExplanation = {
    read_only: true, assessed_at: '2026-10-16T12:00:00Z', status: 'blocked', summary: 'Waiting for current serving proof.',
    source_id: 'source', generation: 3, intent_version: 5, source_freshness: 'stale',
    approved_revision_id: 'approved', applied_revision_id: 'historical',
    workloads: [{ resource: 'workload/report', app_id: 'app', execution_mode: 'job', preparation: 'complete',
      qualification: 'complete', activation: 'complete', serving: 'unknown', blocking_reasons: ['environment_serving_evidence_missing'] }],
    issues: [{ resource: 'workload/report', stage: 'serving', code: 'scheduled_occurrence_unproven', message: 'No current serving proof.',
      actor: 'customer', action: 'Inspect current scheduled task logs.' }],
  };
  const client = new FaaSClient('https://api.example.test', { token: 'token', fetch: async (_input, init) => {
    assert.equal(init?.method, 'GET');
    return Response.json({ source: {}, runs: [], explanation });
  } });
  try {
    const status = await ProjectsService.getEnvironmentGitOps({ slug: 'shop', environment: 'production' });
    assert.deepEqual(status.explanation, explanation);
    assert.equal(status.explanation?.serving_revision_id, undefined);
  } finally { client.uninstall(); }
});

test('GitOps candidate plan keeps read-only observation metadata', async () => {
  const sha = 'a'.repeat(40);
  const client = new FaaSClient('https://api.example.test', {
    token: 'token',
    fetch: async (input, init) => {
      const url = input instanceof Request ? input.url : String(input);
      assert.equal(url, 'https://api.example.test/v1/projects/shop/environments/staging/gitops/plan');
      assert.equal(init?.method, 'POST');
      assert.deepEqual(JSON.parse(String(init?.body)), { commit_sha: sha });
      return Response.json({ source_id: 'source', provider: 'github', project: 'shop', environment: 'staging', observed_at: '2026-10-13T00:00:00Z',
        read_only: true, values_redacted: true, plan: { manager: 'source', revision: `candidate:${sha}`, commit_sha: sha, generation: 0,
          observed_version: 7, desired_digest: 'b'.repeat(64), plan_hash: 'c'.repeat(64), changes: [], blocking_reasons: [] } });
    },
  });
  try {
    const preview = await ProjectsService.previewEnvironmentGitPlan({ slug: 'shop', environment: 'staging', requestBody: { commit_sha: sha } });
    assert.equal(preview.read_only, true);
    assert.equal(preview.values_redacted, true);
    assert.equal(preview.plan.generation, 0);
    assert.equal(preview.plan.observed_version, 7);
  } finally {
    client.uninstall();
  }
});

test('GitOps services preserve reviewed authority and override identity', async () => {
  const sha = 'a'.repeat(40), digest = 'b'.repeat(64);
  const calls: Array<{ url: string; method: string; body: unknown; headers: Headers }> = [];
  const client = new FaaSClient('https://api.example.test', {
    token: 'token',
    fetch: async (input, init) => {
      const url = input instanceof Request ? input.url : String(input);
      calls.push({ url, method: init?.method ?? 'GET', body: init?.body ? JSON.parse(String(init.body)) : null, headers: new Headers(init?.headers) });
      if (url.endsWith('/preview')) {
        return Response.json({ commit_sha: sha, definition_digest: digest, generation: 7,
          definition: { api_version: 'gregale.dev/environment/v1', project: 'shop', environment: 'production', queue_pruning_policy: 'retain', workloads: { api: { app: 'shop-api', queue_bindings: { orders: { queue_name: 'orders', workload_class: 'worker' } }, queue_recoveries: { orders: '11111111-2222-4333-8444-555555555555' } } } } });
      }
      if (url.endsWith('/approve')) return Response.json({}, { status: 202 });
      if (url.endsWith('/gitops')) return Response.json({ source: {
        approved_revision_id: 'approved', source_commit_sha: sha, source_definition_digest: digest,
        source_verified_at: '2026-10-01T00:00:00Z', source_error_code: 'environment_git_source_unavailable',
      }, runs: [], approval: {
        id: 'receipt', source_id: 'source', revision_id: 'approved', generation: 8, definition_digest: digest, recorded_at: '2026-10-01T00:00:00Z',
        evidence: { reviewed_definition_digest: digest, qualified: true, profile: 'reviewed_merge/v1', pull_request_id: 1234, pull_request_number: 7, author_id: 10,
          head_sha: 'c'.repeat(40), merged_at: '2026-09-30T23:00:00Z', checked_at: '2026-10-01T00:00:00Z',
          policy: { qualified: true, profile: 'classic_reviewed_branch/v1', installation_id: 42, repository_id: 123, repository: 'example/shop', branch: 'main', commit_sha: sha, policy_digest: 'd'.repeat(64), required_review_count: 1, checked_at: '2026-10-01T00:00:00Z' },
          reviews: [{ id: 1, reviewer_id: 11, reviewer: 'reviewer', head_sha: 'c'.repeat(40), submitted_at: '2026-09-30T22:00:00Z' }],
        },
      } });
      return new Response(null, { status: 204 });
    },
  });
  try {
    const review = await ProjectsService.previewEnvironmentGitRevision({ slug: 'my project', environment: 'production', requestBody: { commit_sha: sha } });
    assert.equal(review.definition.queue_pruning_policy, 'retain');
    assert.equal(review.definition.workloads.api?.queue_recoveries?.orders, '11111111-2222-4333-8444-555555555555');
    await ProjectsService.approveEnvironmentGitRevision({ slug: 'my project', environment: 'production', requestBody: {
      commit_sha: review.commit_sha, definition_digest: review.definition_digest, expected_generation: review.generation,
    } });
    await ProjectsService.removeEnvironmentGitOpsOverride({ slug: 'my project', environment: 'production', requestBody: { resource: 'workload/api', path: 'variables/MODE' } });
    const status = await ProjectsService.getEnvironmentGitOps({ slug: 'my project', environment: 'production' });
    assert.equal(status.source.source_commit_sha, sha);
    assert.equal(status.source.source_definition_digest, digest);
    assert.equal(status.source.source_verified_at, '2026-10-01T00:00:00Z');
    assert.equal(status.source.source_error_code, 'environment_git_source_unavailable');
    assert.equal(status.source.approved_revision_id, 'approved');
    assert.equal(status.approval?.definition_digest, digest);
    assert.equal(status.approval?.evidence.pull_request_id, 1234);
    assert.equal(status.approval?.evidence.policy.repository_id, 123);
    assert.equal(status.approval?.evidence.reviews[0]?.reviewer_id, 11);
    assert.equal(calls.length, 4);
    assert.ok(calls[0]?.url.includes('/projects/my%20project/environments/production/gitops/revisions/preview'));
    assert.deepEqual(calls[1]?.body, { commit_sha: sha, definition_digest: digest, expected_generation: 7 });
    assert.equal(calls[2]?.method, 'DELETE');
    assert.deepEqual(calls[2]?.body, { resource: 'workload/api', path: 'variables/MODE' });
    for (const call of calls) assert.equal(call.headers.get('Authorization'), 'Bearer token');
  } finally {
    client.uninstall();
  }
});


test('GitLab protected approval uses OAuth repository identity', async () => {
  const client = new FaaSClient('https://api.example.test', {
    token: 'token',
    fetch: async (_input, init) => {
      assert.deepEqual(JSON.parse(String(init?.body)), {
        provider: 'gitlab', repository: 'team/subgroup/shop', ref: 'refs/heads/main',
        manifest_path: 'environments/staging.yaml', mode: 'report', approval_policy: 'protected_branch',
      });
      return Response.json({ id: 'source', source: { provider: 'gitlab', repository_id: 41, installation_id: 0 } });
    },
  });
  try {
    const source = await ProjectsService.createEnvironmentGitSource({ slug: 'shop', environment: 'staging', requestBody: {
      provider: 'gitlab', repository: 'team/subgroup/shop', ref: 'refs/heads/main',
      manifest_path: 'environments/staging.yaml', mode: 'report', approval_policy: 'protected_branch',
    } });
    assert.equal(source.source.provider, 'gitlab');
    assert.equal(source.source.installation_id, 0);
  } finally { client.uninstall(); }
});
