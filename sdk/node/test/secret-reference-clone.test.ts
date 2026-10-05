import test from 'node:test';
import assert from 'node:assert/strict';
import { FaaSClient, ProjectsService } from '../src/index.js';

test('environment clone preserves reference counts and accepts older server responses', async () => {
  const calls: Array<{ url: string; body: unknown; headers: Headers }> = [];
  const client = new FaaSClient('https://api.example.test', {
    token: 'token',
    fetch: async (input, init) => {
      calls.push({ url: input instanceof Request ? input.url : String(input),
        body: JSON.parse(String(init?.body)), headers: new Headers(init?.headers) });
      return Response.json({ id: 'env', project_id: 'project', slug: 'preview', protected: false,
        created_at: '2026-10-01T00:00:00Z', updated_at: '2026-10-01T00:00:00Z', cloned_from: 'production',
        clone: { configuration_copied: false, variables_copied: 0, secrets_copied: 1, workloads_copied: 1,
          bindings_copied: 0, routes_copied: 0, policies_copied: 0, shared_resources: [],
          ...(calls.length === 1 ? { secret_references_copied: 2 } : {}) } }, { status: 201 });
    },
  });
  try {
    const cloned = await ProjectsService.createProjectEnvironment({ slug: 'my project', requestBody: {
      slug: 'preview', from_environment: 'production',
    } });
    assert.equal(cloned.clone?.secret_references_copied, 2);
    assert.equal(cloned.clone?.secrets_copied, 1);
    const legacy = await ProjectsService.createProjectEnvironment({ slug: 'my project', requestBody: {
      slug: 'preview', from_environment: 'production',
    } });
    assert.equal(legacy.clone?.secret_references_copied, undefined);
    for (const call of calls) {
      assert.ok(call.url.endsWith('/projects/my%20project/environments'));
      assert.deepEqual(call.body, { slug: 'preview', from_environment: 'production' });
      assert.equal(call.headers.get('Authorization'), 'Bearer token');
    }
  } finally {
    client.uninstall();
  }
});
