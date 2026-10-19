import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';

function view() {
  const elements = new Map();
  const element = () => ({hidden: false, textContent: '', children: [], replaceChildren() { this.children = []; }, append(child) { this.children.push(child); }});
  const document = {getElementById(id) { if (!elements.has(id)) elements.set(id, element()); return elements.get(id); }, createElement: element};
  const context = vm.createContext({document});
  vm.runInContext(readFileSync(new URL('./web/verification.js', import.meta.url), 'utf8'), context);
  const plan = {id: 'plan', digest: 'digest', scope: {account_id: 'account', app_id: 'app', environment: 'production'},
    source_operation_id: 'source', repair_operation_id: 'repair', applied_at: '2026-10-17T12:00:00Z', candidates: 459, conflicts: 41, unavailable: 0};
  const report = {plan_id: plan.id, plan_digest: plan.digest, scope: plan.scope, source_operation_id: 'source', repair_operation_id: 'repair',
    applied_at: plan.applied_at, checked_at: '2026-10-17T12:05:00Z', restored: 459, excluded: 41, available: true,
    checked: 459, unchanged: 458, edited: 1, missing: 0, issues: [],
    rows: [{row_identity: 'row', key_json: '{"id": 9007199254740993, "label": "<img src=x>"}', expected_version: '3', observed_version: '4', reason: 'record_version_changed'}]};
  return {context, document, plan, report};
}

test('later edits preserve the commit summary and exact keys render as text', () => {
  const {context, document, plan, report} = view();
  assert.equal(context.repairVerificationMatches(plan, report), true);
  context.renderRepairVerification(plan, report);
  assert.match(document.getElementById('verification-receipt').textContent, /459 records restored at commit/);
  assert.match(document.getElementById('verification-receipt').textContent, /41 records were excluded/);
  assert.match(document.getElementById('verification-message').textContent, /458 unchanged.*1 with later edits/);
  assert.equal(document.getElementById('verification-rows').children[0].children[0].textContent,
    '{"id": 9007199254740993, "label": "<img src=x>"} · row row');
});

test('purged journal stays unavailable with historical receipt visible', () => {
  const {context, document, plan, report} = view();
  Object.assign(report, {available: false, checked: 0, unchanged: 0, edited: 0, rows: [], issues: ['journal_purged']});
  assert.equal(context.repairVerificationMatches(plan, report), true);
  context.renderRepairVerification(plan, report);
  assert.match(document.getElementById('verification-message').textContent, /unavailable.*journal was purged/);
  assert.match(document.getElementById('verification-receipt').textContent, /459 records restored at commit/);
  assert.equal(document.getElementById('verification-table').hidden, true);
});

test('mismatched receipts, impossible counts and unavailable zeroes cannot look healthy', () => {
  for (const patch of [{plan_digest: 'other'}, {repair_operation_id: 'other'}, {source_operation_id: 'other'}, {applied_at: 'other'},
    {unchanged: 459}, {available: false}, {scope: {account_id: 'other'}}, {issues: ['journal_purged']}]) {
    const {context, document, plan, report} = view();
    Object.assign(report, patch);
    assert.equal(context.repairVerificationMatches(plan, report), false);
    context.renderRepairVerification(plan, report);
    assert.match(document.getElementById('verification-message').textContent, /did not match/);
    assert.equal(document.getElementById('verification-table').hidden, true);
  }
});
