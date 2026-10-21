import test from 'node:test'
import assert from 'node:assert/strict'
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { mkdtemp, cp, writeFile, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
const runtime = process.env.DATA_API_RUNTIME_DIR
const advisor = runtime ? await import(pathToFileURL(join(runtime, 'dev-explain.mjs'))) : undefined
const execute = promisify(execFile)

test('planner redaction drops expressions and values; candidates include direct RLS equality', { skip: !runtime }, () => {
 const metadata = { tables: new Map([['notes', { columns: ['subject', 'body', 'id'], rows: 30000, policies: ["(subject = (current_setting('request.jwt.claims'::text, true)::jsonb ->> 'sub'::text))"] }]]), indexes: [] }
 const query = advisor.readQuery({ method: 'GET', path: '/rest/v1/notes?body=eq.private-value&order=id.desc', expect: { status: 200 } }, metadata)
 const plan = advisor.redactPlan({ 'Node Type': 'Seq Scan', Schema: 'api', 'Relation Name': 'notes', 'Total Cost': 600, 'Plan Rows': 2, Filter: 'private-value', Output: ['secret'] }, metadata)
 assert.ok(!JSON.stringify(plan).includes('private-value'))
 const advice = advisor.planAdvice(plan, query, metadata)
 assert.deepEqual(advice.suggestions[0].columns.map(item => item.column), ['subject', 'body', 'id'])
 assert.equal(advice.findings[0].severity, 'warning')
 const ordered = { name: 'notes', equality: ['subject'], range: [], order: [{ column: 'body', direction: 'asc' }, { column: 'id', direction: 'desc' }] }
 metadata.indexes = [{ relation: 'notes', method: 'btree', valid: true, partial: false, columns: ['subject', 'body', 'id'], descending: [false, false, false] }]
 assert.equal(advisor.planAdvice(plan, ordered, metadata).suggestions.length, 1)
 metadata.indexes[0].descending = [false, false, true]
 assert.equal(advisor.planAdvice(plan, ordered, metadata).suggestions.length, 0)
 assert.ok(advisor.readQuery({ method: 'POST', path: '/rest/v1/notes' }, metadata).reason)
 assert.ok(advisor.readQuery({ method: 'GET', path: '/rest/v1/notes?id=eq.{{id}}' }, metadata).reason)
 assert.throws(() => advisor.redactPlan({ 'Total Cost': Infinity, 'Plan Rows': 1 }, metadata))
 assert.throws(() => advisor.validatePlanBaseline({ version: 1, status: 'passed' }))
})

test('local explain uses RLS plans, compares indexes and preserves project files', { skip: !process.env.DATA_API_EXPLAIN_NATIVE || !runtime, timeout: 240000 }, async t => {
 const inventory = async () => (await execute('docker', ['container', 'ls', '--all', '--filter', 'label=gregale.data-api.dev=true', '--format', '{{.Names}}'])).stdout.trim().split('\n').filter(Boolean).sort()
 const volumes = async () => (await execute('docker', ['volume', 'ls', '--format', '{{.Name}}'])).stdout.trim().split('\n').filter(Boolean).sort()
 const initialContainers = await inventory(), initialVolumes = await volumes()
 const directory = await mkdtemp(join(tmpdir(), 'gregale-explain-test-'))
 t.after(() => rm(directory, { recursive: true, force: true }))
 await cp(process.env.DATA_API_STARTER_DIR, directory, { recursive: true })
 const output = join(directory, 'client/src/database.types.ts'), original = await readFile(output, 'utf8')
 const fixture = join(directory, 'fixture.sql')
 await writeFile(fixture, "INSERT INTO api.notes(subject,body) SELECT CASE WHEN n%200=0 THEN 'alice' ELSE 'other-'||(n%200)::text END, 'private-marker-'||n::text FROM generate_series(1,40000) n;\n")
 const collection = join(directory, 'plans.json')
 await writeFile(collection, JSON.stringify({ version: 1, requests: [
  { name: 'Selective read', method: 'GET', path: '/rest/v1/notes?body=eq.private-marker-200&order=id.desc', identity: 'alice', expect: { status: 200 } },
  { name: 'Write skipped', method: 'POST', path: '/rest/v1/notes', identity: 'alice', body: { subject: 'alice', body: 'not-executed' }, expect: { status: 201 } }
 ] }))
 const args = ['--json', 'data-api', 'explain', '--directory', directory, '--collection', collection, '--fixture', fixture]
 const run = async extra => JSON.parse((await execute(process.env.DATA_API_DEV_CLI, [...args, ...extra], { timeout: 90000, maxBuffer: 1 << 20 })).stdout)
 const before = await run([])
 assert.equal(before.status, 'passed'); assert.equal(before.cleanup, 'passed')
 assert.equal(before.summary.planned, 1); assert.equal(before.summary.skipped, 1)
 assert.ok(!JSON.stringify(before).includes('private-marker'))
 assert.ok(before.results[0].suggestions.length)
 const baseline = join(directory, 'before.json'); await writeFile(baseline, JSON.stringify(before))
 await writeFile(join(directory, 'migrations/sql/0017_advisor_index.sql'), before.results[0].suggestions[0].sql)
 const after = await run(['--baseline', baseline])
 assert.equal(after.status, 'passed')
 assert.ok(after.results[0].comparison.after_cost < after.results[0].comparison.before_cost)
 const budget = join(directory, 'budgets.json')
 const selector = { scenario: before.results[0].scenario, name: before.results[0].name }
 await writeFile(budget, JSON.stringify({ version: 1, queries: [{ ...selector, max_total_cost: before.results[0].plan.total_cost, max_cost_ratio: 1, max_scanned_rows: 40000 }] }))
 const checked = await run(['--baseline', baseline, '--check', '--budget', budget])
 assert.equal(checked.budget_check.status, 'passed')
 await writeFile(budget, JSON.stringify({ version: 1, queries: [{ ...selector, max_total_cost: 0, max_scanned_rows: 0 }] }))
 await assert.rejects(run(['--baseline', baseline, '--check', '--budget', budget]), error => {
  const report = JSON.parse(error.stdout)
  assert.equal(error.code, 1); assert.equal(report.status, 'failed'); assert.equal(report.cleanup, 'passed')
  assert.equal(report.budget_check.status, 'failed'); assert.ok(report.budget_check.violations.some(item => item.metric === 'max_total_cost'))
  assert.ok(!error.stdout.includes('private-marker')); return true
 })
 await writeFile(budget, JSON.stringify({ version: 1, queries: [{ ...selector, max_cost_ratio: 'invalid' }] }))
 await assert.rejects(run(['--baseline', baseline, '--check', '--budget', budget]), error => !error.stdout && !error.stderr.includes('private-marker'))
 assert.equal(await readFile(output, 'utf8'), original)
 await writeFile(fixture, 'SELECT 1;')
 await assert.rejects(run(['--baseline', baseline]))
 await writeFile(collection, JSON.stringify({ version: 1, requests: [{ name: 'Writes only', method: 'POST', path: '/rest/v1/notes', identity: 'alice', body: { subject: 'alice', body: 'not-executed' }, expect: { status: 201 } }] }))
 await assert.rejects(run([]), error => { const report = JSON.parse(error.stdout); assert.equal(report.status, 'failed'); assert.equal(report.summary.planned, 0); assert.equal(report.cleanup, 'passed'); return true })
 assert.deepEqual(await inventory(), initialContainers); assert.deepEqual(await volumes(), initialVolumes)
})

test('query budgets validate strict bounded selectors and fail closed on coverage and zero baselines', { skip: !runtime }, () => {
 const selector = { scenario: 'default', name: 'read' }
 const rule = { ...selector, max_total_cost: 10, max_cost_ratio: 2, max_scanned_rows: 20 }
 const budget = advisor.queryBudgets({ version: 1, queries: [rule] })
 const planned = { ...selector, status: 'planned', plan: { total_cost: 10 }, metrics: { estimated_scan_rows: 20 }, comparison: { before_cost: 5 } }
 assert.equal(advisor.checkQueryBudgets(budget, [planned]).status, 'passed')
 const failed = advisor.checkQueryBudgets(budget, [{ ...planned, plan: { total_cost: 11 }, metrics: { estimated_scan_rows: 21 } }])
 assert.equal(failed.violations.length, 3)
 assert.equal(advisor.checkQueryBudgets(budget, [{ ...planned, comparison: { before_cost: 0 } }]).violations[0].code, 'cost_ratio_exceeded')
 assert.equal(advisor.checkQueryBudgets(budget, [{ ...planned, plan: { total_cost: 0 }, comparison: { before_cost: 0 } }]).status, 'passed')
 assert.equal(advisor.checkQueryBudgets(budget, [{ ...planned, comparison: null }]).violations[0].code, 'missing_baseline_query')
 assert.equal(advisor.checkQueryBudgets(budget, [{ ...planned, status: 'skipped' }]).violations[0].code, 'query_not_planned')
 assert.equal(advisor.checkQueryBudgets(budget, [{ ...planned, name: 'other' }]).violations[0].code, 'missing_budget')
 assert.equal(advisor.checkQueryBudgets(budget, [{ ...planned, metrics: null }]).violations[0].code, 'metric_unavailable')
 for (const value of [null, { version: 1, queries: [] }, { version: 2, queries: [rule] }, { version: 1, queries: [rule, rule] }, { version: 1, queries: [{ ...selector }] }, { version: 1, queries: [{ ...rule, max_cost_ratio: 0 }] }, { version: 1, queries: [{ ...rule, max_total_cost: -1 }] }, { version: 1, queries: [{ ...rule, max_scanned_rows: '20' }] }, { version: 1, queries: [{ ...rule, extra: true }] }, { version: 1, queries: Array(101).fill(rule) }]) assert.throws(() => advisor.queryBudgets(value))
})
