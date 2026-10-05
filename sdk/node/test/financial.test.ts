import test from 'node:test';
import assert from 'node:assert/strict';
import { BillingService } from '../src/generated/services/BillingService.js';
import { ApiError } from '../src/generated/core/ApiError.js';
import { OpenAPI } from '../src/generated/core/OpenAPI.js';
import type { FinancialBudgetSpec } from '../src/generated/models/FinancialBudgetSpec.js';

test('financial reads preserve month, authentication, exact money and source gaps', async () => {
  const originalFetch = globalThis.fetch;
  const originalBase = OpenAPI.BASE;
  const originalToken = OpenAPI.TOKEN;
  const period = { period_start: '2026-10-01T00:00:00Z', period_end: '2026-11-01T00:00:00Z', as_of: '2026-10-02T00:00:00Z', currency: 'EUR', meters: [], missing_bill_components: ['tax'] };
  try {
    OpenAPI.BASE = 'https://api.example.com';
    OpenAPI.TOKEN = 'financial-reader';
    globalThis.fetch = async (input, options) => {
      const url = new URL(String(input));
      assert.equal(url.searchParams.get('month'), '2026-10');
      assert.equal(options?.method, 'GET');
      assert.equal(options?.body, undefined);
      assert.equal(new Headers(options?.headers).get('Authorization'), 'Bearer financial-reader');
      if (url.pathname === '/v1/billing/costs') {
        return Response.json({ ...period, account_id: 'account', retained_from: period.period_start, evidence_through_id: 7, known_usage_millicents: 10001, scope: 'retained_compute_and_interface_egress', invoices: [], invoice_reconciliation: 'not_reconciled' });
      }
      assert.equal(url.pathname, '/v1/billing/forecast');
      return Response.json({ ...period, bill_estimate_available: false });
    };
    const costs = await BillingService.getFinancialCosts({ month: '2026-10' });
    assert.equal(costs.known_usage_millicents, 10001);
    assert.equal(costs.invoice_reconciliation, 'not_reconciled');
    const forecast = await BillingService.getFinancialForecast({ month: '2026-10' });
    assert.equal(forecast.bill_estimate_available, false);
    assert.deepEqual(forecast.missing_bill_components, ['tax']);
    globalThis.fetch = async () => Response.json({ status: 503, title: 'Unavailable', code: 'capacity' }, { status: 503 });
    await assert.rejects(BillingService.getFinancialCosts({ month: '2026-10' }), (error: unknown) => error instanceof ApiError && error.status === 503 && error.body.code === 'capacity');
  } finally {
    globalThis.fetch = originalFetch;
    OpenAPI.BASE = originalBase;
    OpenAPI.TOKEN = originalToken;
  }
});

test('budget previews preserve proposed actions and unavailable enforcement', async () => {
  const originalFetch = globalThis.fetch;
  const spec: FinancialBudgetSpec = { name: 'Preview guard', scope: { kind: 'account' }, currency: 'EUR', meters: ['compute'], basis: 'net_usage', limit_millicents: 10001, notify_millicents: [8000], mode: 'monitored', action: 'stop_previews', drain_seconds: 30, resume_rule: 'manual', enabled: true };
  try {
    globalThis.fetch = async (input, options) => {
      assert.equal(new URL(String(input)).pathname, '/v1/billing/budgets/preview');
      assert.equal(options?.method, 'POST');
      assert.deepEqual(JSON.parse(String(options?.body)), { spec });
      return Response.json({ spec, period_start: '2026-10-01T00:00:00Z', period_end: '2026-11-01T00:00:00Z', as_of: '2026-10-02T00:00:00Z', known_millicents: 75, known_limit_reached: false, coverage_complete: false, fresh: true, reasons: ['enforcement_integration_pending'], enforcement_ready: false, guarantee: 'monitored_after_retained_evidence', targets: [{ kind: 'app', id: 'a', name: 'preview', effect: 'block_traffic_and_wakes_then_park_at_drain_deadline' }], continuing_targets: [{ kind: 'app', id: 'b', name: 'production', effect: 'compute_can_continue' }] });
    };
    const result = await BillingService.previewFinancialBudget({ requestBody: { spec } });
    assert.equal(result.enforcement_ready, false);
    assert.equal(result.known_millicents, 75);
    assert.equal(result.targets[0]?.name, 'preview');
    assert.equal(result.continuing_targets[0]?.name, 'production');
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test('budget policy clients preserve revision preconditions, retries, history and activation errors', async () => {
  const originalFetch = globalThis.fetch;
  const originalBase = OpenAPI.BASE;
  const id = '6dc4f678-5766-4a06-a061-845c2b133fdd';
  const spec: FinancialBudgetSpec = { name: 'Draft', scope: { kind: 'account' }, currency: 'EUR', meters: ['compute'], basis: 'net_usage', limit_millicents: 1000001, notify_millicents: [], mode: 'monitored', action: 'stop_previews', drain_seconds: 30, resume_rule: 'manual', enabled: false };
  const policy = { id, account_id: id, revision: 7, spec, created_at: '2026-10-01T00:00:00Z', updated_at: '2026-10-02T00:00:00Z', status: 'draft', enforcement_ready: false, reasons: ['enforcement_integration_pending'] };
  let calls = 0;
  try {
    OpenAPI.BASE = 'https://api.example.com';
    globalThis.fetch = async (input, options) => {
      calls++;
      const url = new URL(String(input));
      if (options?.method === 'POST' || options?.method === 'PUT' || options?.method === 'DELETE') {
        assert.equal(new Headers(options?.headers).get('Idempotency-Key'), 'stable-retry');
        const body = JSON.parse(String(options?.body));
        if (options.method !== 'POST') assert.equal(body.expected_revision, 7);
        if (options.method !== 'DELETE') assert.deepEqual(body.spec, spec);
        return Response.json(policy, { status: options.method === 'POST' ? 201 : 200 });
      }
      if (url.pathname.endsWith('/revisions')) {
        assert.equal(url.searchParams.get('after_revision'), '3');
        assert.equal(url.searchParams.get('limit'), '2');
        return Response.json({ revisions: [], next_revision: 5 });
      }
      return Response.json(url.pathname.endsWith('/budgets') ? { budgets: [policy] } : policy);
    };
    assert.equal((await BillingService.listFinancialBudgets()).budgets[0]?.enforcement_ready, false);
    assert.equal((await BillingService.getFinancialBudget({ id })).revision, 7);
    assert.equal((await BillingService.createFinancialBudget({ idempotencyKey: 'stable-retry', requestBody: { spec } })).spec.limit_millicents, 1000001);
    await BillingService.updateFinancialBudget({ id, idempotencyKey: 'stable-retry', requestBody: { expected_revision: 7, spec } });
    await BillingService.deleteFinancialBudget({ id, idempotencyKey: 'stable-retry', requestBody: { expected_revision: 7 } });
    assert.equal((await BillingService.listFinancialBudgetRevisions({ id, afterRevision: 3, limit: 2 })).next_revision, 5);
    assert.equal(calls, 6);
    globalThis.fetch = async () => Response.json({ status: 422, code: 'financial_budget_activation_unavailable', title: 'Unavailable' }, { status: 422 });
    await assert.rejects(BillingService.createFinancialBudget({ idempotencyKey: 'stable-retry', requestBody: { spec: { ...spec, enabled: true } } }), (error: unknown) => error instanceof ApiError && error.body.code === 'financial_budget_activation_unavailable');
  } finally {
    globalThis.fetch = originalFetch;
    OpenAPI.BASE = originalBase;
  }
});
