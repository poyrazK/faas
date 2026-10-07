import test from 'node:test';
import assert from 'node:assert/strict';
import { FaaSClient, QueuesService } from '../src/index.js';

test('retained queue history preserves recovery UUID and retirement timestamp', async () => {
  const id = '11111111-2222-4333-8444-555555555555';
  const retiredAt = '2026-10-01T22:50:12Z';
  const calls: URL[] = [];
  const client = new FaaSClient('https://api.example.test', {
    token: 'token',
    fetch: async (input, init) => {
      const url = new URL(input instanceof Request ? input.url : String(input));
      calls.push(url);
      assert.equal(url.pathname, '/v1/apps/worker/queue-bindings');
      assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer token');
      return Response.json(url.searchParams.get('include_retired') === 'true'
        ? [{ id, environment: 'production', retired_at: retiredAt }] : []);
    },
  });
  try {
    assert.deepEqual(await QueuesService.listQueueBindings({ slug: 'worker' }), []);
    const history = await QueuesService.listQueueBindings({ slug: 'worker', includeRetired: true });
    assert.equal(history.length, 1);
    assert.equal(history[0]?.id, id);
    assert.equal(history[0]?.retired_at, retiredAt);
    assert.equal(calls.length, 2);
    assert.notEqual(calls[0]?.searchParams.get('include_retired'), 'true');
    assert.equal(calls[1]?.searchParams.get('include_retired'), 'true');
  } finally {
    client.uninstall();
  }
});
