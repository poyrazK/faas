import test from 'node:test';
import assert from 'node:assert/strict';
import { FaaSClient, QueuesService } from '../src/index.js';

test('queue producers and bindings preserve environment selection and identity', async () => {
  const bindingID = '00000000-0000-4000-8000-000000000001';
  const environmentID = '00000000-0000-4000-8000-000000000002';
  const calls: Array<{ url: string; body: Record<string, unknown> | null }> = [];
  const client = new FaaSClient('https://api.example.test', {
    token: 'token',
    fetch: async (input, init) => {
      const url = input instanceof Request ? input.url : String(input);
      calls.push({ url, body: init?.body ? JSON.parse(String(init.body)) : null });
      if (url.endsWith('/status')) return Response.json({ binding_id: bindingID, environment: 'staging', environment_id: environmentID, consumer_state: 'paused', consumer_state_reason: 'environment_unavailable', consumer_liveness: 'not_observed' });
      return Response.json({ id: bindingID, environment: 'staging', environment_id: environmentID, queue_binding_id: bindingID, event_id: 'event', target_app: 'worker', status: 'pending', status_url: '/status' }, { status: url.endsWith('/inbox') ? 202 : 201 });
    },
  });
  try {
    const binding = await QueuesService.createQueueBinding({ slug: 'worker', requestBody: { environment: 'staging', name: 'orders', queue_name: 'orders' } });
    assert.equal(binding.environment_id, environmentID);
    assert.equal(binding.environment, 'staging');
    const sent = await QueuesService.queueSend({ slug: 'worker', requestBody: { environment: 'staging', queue_name: 'orders', payload: {}, flag_context: 'flags' } });
    assert.equal(sent.queue_binding_id, bindingID);
    assert.equal(sent.environment, 'staging');
    const inbox = await QueuesService.sendAppMessage({ slug: 'worker', requestBody: { environment: 'staging', queue_name: 'orders', type: 'order.created', data: {} } });
    assert.equal(inbox.queue_binding_id, bindingID);
    assert.equal(inbox.environment, 'staging');
    const status = await QueuesService.getQueueBindingStatus({ slug: 'worker', id: bindingID });
    assert.equal(status.environment_id, environmentID);
    assert.equal(status.consumer_state_reason, 'environment_unavailable');
    assert.equal(calls.length, 4);
    for (const call of calls.slice(0, 3)) assert.equal(call.body?.environment, 'staging');
    assert.equal(calls[1]?.body?.flag_context, 'flags');
  } finally {
    client.uninstall();
  }
});
