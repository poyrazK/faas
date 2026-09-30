import test from 'node:test';
import assert from 'node:assert/strict';
import { BillingService } from '../src/generated/services/BillingService.js';
import { ApiError } from '../src/generated/core/ApiError.js';
import { OpenAPI } from '../src/generated/core/OpenAPI.js';

for (const [format, contentType] of [['zip', 'application/zip'], ['csv', 'text/csv'], ['metadata', 'application/json']] as const) {
  test(`FOCUS ${format} download preserves bytes and authentication`, async () => {
    const originalFetch = globalThis.fetch;
    const originalBase = OpenAPI.BASE;
    const originalToken = OpenAPI.TOKEN;
    const bytes = format === 'zip' ? new Uint8Array([80, 75, 0, 255, 128, 10]) : new TextEncoder().encode(format === 'csv' ? 'BilledCost,BillingAccountId\n1.00,account-1\n' : '{"ConformanceStatus":"partial"}\n');
    try {
      OpenAPI.BASE = 'https://api.example.com';
      OpenAPI.TOKEN = 'focus-token';
      globalThis.fetch = async (input, options) => {
        const url = new URL(String(input));
        assert.equal(url.pathname, '/v1/billing/focus');
        assert.equal(url.searchParams.get('month'), '2026-09');
        assert.equal(url.searchParams.get('format'), format);
        assert.equal(new Headers(options?.headers).get('Authorization'), 'Bearer focus-token');
        return new Response(bytes, {headers: {'Content-Type': contentType}});
      };
      const body = await BillingService.exportFocusInvoices({month: '2026-09', format});
      assert.ok(body instanceof Blob);
      assert.deepEqual(new Uint8Array(await body.arrayBuffer()), bytes);
    } finally {
      globalThis.fetch = originalFetch;
      OpenAPI.BASE = originalBase;
      OpenAPI.TOKEN = originalToken;
    }
  });
}

test('FOCUS downloads retain structured Problem errors', async () => {
  const originalFetch = globalThis.fetch;
  try {
    globalThis.fetch = async () => new Response(JSON.stringify({status: 409, title: 'Export unavailable', code: 'conflict'}), {status: 409, headers: {'Content-Type': 'application/problem+json'}});
    await assert.rejects(BillingService.exportFocusInvoices({month: '2026-09'}), (error: unknown) => error instanceof ApiError && error.status === 409 && error.body.code === 'conflict');
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test('FOCUS invoice refresh sends an authenticated empty POST and retains source gaps', async () => {
  const originalFetch = globalThis.fetch;
  const originalBase = OpenAPI.BASE;
  const originalToken = OpenAPI.TOKEN;
  const id = 'c4979a3e-345b-4a96-a635-321589233f7f';
  try {
    OpenAPI.BASE = 'https://api.example.com';
    OpenAPI.TOKEN = 'focus-token';
    globalThis.fetch = async (input, options) => {
      const url = new URL(String(input));
      assert.equal(url.pathname, `/v1/invoices/${id}/refresh`);
      assert.equal(url.search, '');
      assert.equal(options?.method, 'POST');
      assert.equal(options?.body, undefined);
      assert.equal(new Headers(options?.headers).get('Authorization'), 'Bearer focus-token');
      return Response.json({invoice_id: id, provider: 'polar', detailed: false, line_items: 2, source_gap: 'unclassified', updated_at: '2026-10-01T00:00:00Z'});
    };
    const result = await BillingService.refreshInvoiceFacts({id});
    assert.equal(result.invoice_id, id);
    assert.equal(result.source_gap, 'unclassified');
    globalThis.fetch = async () => new Response(JSON.stringify({status: 409, title: 'Refresh conflict', code: 'conflict'}), {status: 409, headers: {'Content-Type': 'application/problem+json'}});
    await assert.rejects(BillingService.refreshInvoiceFacts({id}), (error: unknown) => error instanceof ApiError && error.status === 409 && error.body.code === 'conflict');
  } finally {
    globalThis.fetch = originalFetch;
    OpenAPI.BASE = originalBase;
    OpenAPI.TOKEN = originalToken;
  }
});
