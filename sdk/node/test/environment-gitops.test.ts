import test from 'node:test';
import assert from 'node:assert/strict';
import { FaaSClient, ProjectsService } from '../src/index.js';

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
          definition: { api_version: 'gregale.dev/environment/v1', project: 'shop', environment: 'production', workloads: { api: { app: 'shop-api' } } } });
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
