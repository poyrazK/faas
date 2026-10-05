import test from 'node:test';
import assert from 'node:assert/strict';
import { FaaSClient } from '../src/index.js';
import { InternalService } from '../src/generated/services/InternalService.js';

test('queue batch transport preserves durable identity and per-record outcome', async () => {
  const id = '33333333-3333-3333-3333-333333333333';
  const body = {
    invocation_id: 'trigger-test', app_id: '22222222-2222-2222-2222-222222222222',
    trigger_id: '11111111-1111-1111-1111-111111111111', source: 'esm',
    records: [
      { item_identifier: id, invocation_id: id, invocation_attempt: 2, payload_b64: 'e30=' },
      { item_identifier: 'broker-record', payload_b64: 'e30=' },
    ],
  };
  const client = new FaaSClient('https://api.example.test', {
    token: 'token',
    fetch: async (input, init) => {
      const url = input instanceof Request ? input.url : String(input);
      assert.equal(url, 'https://api.example.test/v1/invocations:dispatch_batch');
      assert.equal(init?.method, 'POST');
      assert.deepEqual(JSON.parse(String(init?.body)), body);
      return Response.json({ results: [
        { item_identifier: id, status: 'succeeded' },
        { item_identifier: 'broker-record', status: 'retry', code: 'invoke_timeout' },
      ] });
    },
  });
  try {
    const response = await InternalService.dispatchInvocationBatch({ requestBody: body });
    assert.equal(response.results[0]?.status, 'succeeded');
    assert.equal(response.results[1]?.code, 'invoke_timeout');
  } finally {
    client.uninstall();
  }
});
