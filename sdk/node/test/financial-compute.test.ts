import test from 'node:test';
import assert from 'node:assert/strict';
import { BillingService } from '../src/generated/services/BillingService.js';

test('strict compute reads retain exact funding and unconfirmed retirement', async () => {
  const originalFetch = globalThis.fetch;
  const id = '6dc4f678-5766-4a06-a061-845c2b133fdd';
  const start = '2026-10-01T00:00:00Z';
  const compute = { initialized: true, app_id: id, period_start: start, period_end: '2026-11-01T00:00:00Z', committed_millicents: 9007199254740000, reserved_millicents: 991, remaining_millicents: 0, unconfirmed_leases: 1, held: true, observed_at: '2026-10-17T10:00:00Z' };
  try {
    globalThis.fetch = async () => Response.json({ id, account_id: id, revision: 2, created_at: start, updated_at: start, spec: { name: 'App compute', scope: { kind: 'app', id }, currency: 'USD', meters: ['compute'], basis: 'gross_usage', limit_millicents: Number.MAX_SAFE_INTEGER, notify_millicents: [], mode: 'strict', action: 'suspend_workloads', drain_seconds: 0, resume_rule: 'manual', enabled: true }, status: 'active', enforcement_ready: true, notification_ready: false, reasons: ['conservative_compute_commitments'], runtime: { compute, alerts: [] } });
    const budget = await BillingService.getFinancialBudget({ id });
    assert.deepEqual(budget.runtime?.compute, compute);
    assert.equal(budget.notification_ready, false);
    assert.equal(budget.runtime?.compute?.committed_millicents! + budget.runtime?.compute?.reserved_millicents!, Number.MAX_SAFE_INTEGER);
    assert.equal(budget.runtime?.evaluation, undefined);
  } finally { globalThis.fetch = originalFetch; }
});

test('account compute exposes shared allowance without an app identity', async () => {
  const originalFetch = globalThis.fetch;
  const id = '6dc4f678-5766-4a06-a061-845c2b133fdd';
  const compute = { initialized: true, account_id: id, scope_kind: 'account', basis: 'net_usage', period_start: '2026-10-01T00:00:00Z', period_end: '2026-11-01T00:00:00Z', committed_millicents: 1000, reserved_millicents: 25, remaining_millicents: 975, allowance_granted_mb_seconds: Number.MAX_SAFE_INTEGER, allowance_committed_mb_seconds: 9007199254740000, allowance_reserved_mb_seconds: 991, allowance_remaining_mb_seconds: 0, unconfirmed_leases: 2, held: false, observed_at: '2026-10-17T10:00:00Z' };
  try {
    globalThis.fetch = async () => Response.json({ id, runtime: { compute, alerts: [] } });
    const budget = await BillingService.getFinancialBudget({ id });
    assert.deepEqual(budget.runtime?.compute, compute);
    assert.equal(budget.runtime?.compute?.app_id, undefined);
    assert.equal(budget.runtime?.compute?.allowance_granted_mb_seconds, Number.MAX_SAFE_INTEGER);
  } finally { globalThis.fetch = originalFetch; }
});

test('job compute reads retain job identity, gross spending and a queued-work hold', async () => {
  const originalFetch = globalThis.fetch;
  const id = '6dc4f678-5766-4a06-a061-845c2b133fdd';
  const compute = { initialized: true, job_id: id, scope_kind: 'job', basis: 'gross_usage', period_start: '2026-10-01T00:00:00Z', period_end: '2026-11-01T00:00:00Z', committed_millicents: 9007199254740000, reserved_millicents: 991, remaining_millicents: 0, unconfirmed_leases: 1, held: true, observed_at: '2026-10-17T10:00:00Z' };
  try {
    globalThis.fetch = async () => Response.json({ id, runtime: { compute, alerts: [] } });
    const budget = await BillingService.getFinancialBudget({ id });
    assert.deepEqual(budget.runtime?.compute, compute);
    assert.equal(budget.runtime?.compute?.job_id, id);
    assert.equal(budget.runtime?.compute?.scope_kind, 'job');
    assert.equal(budget.runtime?.compute?.app_id, undefined);
  } finally { globalThis.fetch = originalFetch; }
});

test('shared account limit retains separate conservative egress funding', async () => {
  const originalFetch = globalThis.fetch;
  const id = '6dc4f678-5766-4a06-a061-845c2b133fdd';
  const egress = { initialized: true, committed_bytes: Number.MAX_SAFE_INTEGER - 991, reserved_bytes: 991, allowance_granted_bytes: Number.MAX_SAFE_INTEGER, allowance_committed_bytes: Number.MAX_SAFE_INTEGER - 991, allowance_reserved_bytes: 991, allowance_remaining_bytes: 0, committed_millicents: 1000, reserved_millicents: 25 };
  try {
    globalThis.fetch = async () => Response.json({ id, runtime: { compute: { egress, committed_millicents: 2000, reserved_millicents: 50, remaining_millicents: 0, held: true }, alerts: [] } });
    const budget = await BillingService.getFinancialBudget({ id });
    assert.deepEqual(budget.runtime?.compute?.egress, egress);
    assert.equal(budget.runtime?.compute?.egress?.committed_bytes! + budget.runtime?.compute?.egress?.reserved_bytes!, Number.MAX_SAFE_INTEGER);
    assert.equal(budget.runtime?.compute?.committed_millicents, 2000);
    assert.equal(budget.runtime?.compute?.held, true);
  } finally { globalThis.fetch = originalFetch; }
});
