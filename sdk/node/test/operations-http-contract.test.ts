// ADR-521: the public generated client preserves stable submissions and durable cursors.
import test from 'node:test';
import assert from 'node:assert/strict';
import { OperationsService, OpenAPI } from '../src/index.js';

test('HTTP Operations contract uses customer credentials, stable keys and resume cursors', async () => {
  const previousFetch = globalThis.fetch;
  const previousBase = OpenAPI.BASE;
  const previousToken = OpenAPI.TOKEN;
  const calls: { url: string; headers: Headers }[] = [];
  OpenAPI.BASE = 'https://api.example.com';
  OpenAPI.TOKEN = 'customer-token';
  globalThis.fetch = async (input, init) => {
    calls.push({ url: String(input), headers: new Headers(init?.headers) });
    return new Response(JSON.stringify({ id: 'export-id', latest_sequence: 42, events: [] }), {
      headers: { 'Content-Type': 'application/json' },
    });
  };
  try {
    const receipt = await OperationsService.startPlatformTenantSelfOperation({
      idempotencyKey: 'stable-export', requestBody: { definition_id: 'definition-id', input: { count: 1 } },
    });
    assert.equal(receipt.id, 'export-id');
    assert.equal(calls[0]?.headers.get('Idempotency-Key'), 'stable-export');
    assert.equal(calls[0]?.headers.get('Authorization'), 'Bearer customer-token');
    assert.equal(calls[0]?.url, 'https://api.example.com/v1/platform-tenant-self/customer-operations');
    await OperationsService.getPlatformTenantSelfOperationEvents({ id: 'export-id', after: 42 });
    assert.equal(calls[1]?.url, 'https://api.example.com/v1/platform-tenant-self/customer-operations/export-id/events?after=42');
  } finally {
    globalThis.fetch = previousFetch;
    OpenAPI.BASE = previousBase;
    OpenAPI.TOKEN = previousToken;
  }
});
