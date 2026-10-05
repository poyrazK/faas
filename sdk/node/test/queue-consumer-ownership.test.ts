import test from 'node:test';
import assert from 'node:assert/strict';
import { FaaSClient, TriggersService, isFaasError } from '../src/index.js';

test('trigger controls return updated state and preserve binding ownership conflicts', async () => {
  const client = new FaaSClient('https://api.example.test', {
    token: 'token',
    fetch: async (input) => {
      const url = input instanceof Request ? input.url : String(input);
      if (url.includes('/triggers/consumer')) {
        return Response.json({ title: 'Queue consumer is binding-owned', status: 409,
          code: 'validation_failed', detail: 'manage this consumer through its queue binding' },
        { status: 409, headers: { 'Content-Type': 'application/problem+json' } });
      }
      return Response.json({ id: 'broker', account_id: 'account', app_id: 'app', kind: 'queue',
        slug: 'jobs', enabled: url.endsWith('/resume'), config: { mode: 'queue' },
        batch_size_max: 1, batch_window_ms: 1000, max_attempts: 3, payload_max_bytes: 1024,
        broker_poison_strategy: 'commit', created_at: '2026-10-01T00:00:00Z', updated_at: '2026-10-01T00:00:00Z' });
    },
  });
  try {
    const paused = await TriggersService.pauseTrigger({ id: 'broker' });
    const resumed = await TriggersService.resumeTrigger({ id: 'broker' });
    assert.equal(paused.enabled, false);
    assert.equal(resumed.enabled, true);
    const mutations = [
      () => TriggersService.pauseTrigger({ id: 'consumer' }),
      () => TriggersService.resumeTrigger({ id: 'consumer' }),
      () => TriggersService.updateTrigger({ id: 'consumer', requestBody: { enabled: false } }),
      () => TriggersService.deleteTrigger({ id: 'consumer' }),
    ];
    for (const mutate of mutations) {
      await assert.rejects(mutate(), (error: unknown) => {
        assert.ok(isFaasError(error));
        assert.equal(error.status, 409);
        assert.equal(error.problem.code, 'validation_failed');
        return true;
      });
    }
  } finally {
    client.uninstall();
  }
});
