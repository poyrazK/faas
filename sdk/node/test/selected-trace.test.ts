// ADR-20261017233500: repeat app IDs and preserve source failures without fallback.
import assert from 'node:assert/strict';
import test from 'node:test';
import { AccountService, OpenAPI } from '../src/preview.js';

test('selected trace sends exact repeated app IDs and never falls back to account lookup', async () => {
  const oldFetch = globalThis.fetch;
  const oldBase = OpenAPI.BASE;
  const ids = ['11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222'];
  const trace = '4bf92f3577b34da6a3ce929d0e0e4736';
  OpenAPI.BASE = 'https://api.invalid';
  try {
    for (const status of [200, 404]) {
      let calls = 0;
      globalThis.fetch = async (input, init) => {
        calls++;
        const url = new URL(String(input));
        assert.equal(init?.method, 'GET');
        assert.equal(url.pathname, `/v1/account/traces/${trace}/selected-apps`);
        assert.deepEqual(url.searchParams.getAll('app_id'), ids);
        assert.equal(url.searchParams.get('limit'), '7');
        return new Response(JSON.stringify({ selection: 'explicit_app_ids_v1', trace_id: trace,
          apps: [{ id: ids[0], slug: 'alpha' }], generated_at: '2026-10-17T11:00:00Z', limit: 7,
          request_window_start: '2026-10-14T11:00:00Z', request_window_end: '2026-10-17T11:00:00Z', request_retention_days: 3,
          requests: [], requests_status: 'unavailable', requests_truncated: false,
          invocations: [], invocations_status: 'ok', invocations_truncated: false, partial: true,
        }), { status, headers: { 'Content-Type': 'application/json' } });
      };
      const request = AccountService.getSelectedAppTrace({ traceId: trace, appId: ids, limit: 7 });
      if (status === 404) await assert.rejects(request, (error: { status?: number }) => error.status === 404);
      else {
        const result = await request;
        assert.equal(result.partial, true);
        assert.equal(result.requests_status, 'unavailable');
        assert.deepEqual(result.requests, []);
      }
      assert.equal(calls, 1);
    }
  } finally {
    globalThis.fetch = oldFetch;
    OpenAPI.BASE = oldBase;
  }
});
