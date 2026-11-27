import assert from 'node:assert/strict';
import { test } from 'node:test';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import sales, { parseCSV } from '../sales-report/handler.mjs';
import merge from '../report-pipeline/handler.mjs';
import { buildExamples, root, jsonFile, names, pipelineArguments } from '../scripts/manifests.mjs';
import { checkRecovery } from '../scripts/check-recovery.mjs';

async function scratch(t) {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'gregale-run-example-'));
  t.after(() => fs.rm(dir, { recursive: true, force: true }));
  return { input_dir: dir, output_dir: dir };
}
const order = (overrides = {}) => ({ order_id: 'one', region: 'north', quantity: 3, unit_price_cents: 1299, ...overrides });

test('CSV report returns exact cents and both declared exports', async t => {
  const context = await scratch(t); context.input_dir = path.join(root, 'sales-report');
  assert.deepEqual(await sales(await jsonFile('sales-report/input.json'), context), await jsonFile('sales-report/expected.json'));
  const csv = await fs.readFile(path.join(context.output_dir, 'summary.csv'), 'utf8');
  assert.deepEqual(parseCSV(csv), [['region', 'orders', 'units', 'revenue_cents'], ['north', '2', '5', '4098'], ['south', '2', '3', '2898']]);
  assert.match(await fs.readFile(path.join(context.output_dir, 'report.html'), 'utf8'), /6996 USD cents/);
  assert.deepEqual(parseCSV('id,value\r\n"a,""b",3\r\n'), [['id', 'value'], ['a,"b', '3']]);
});

test('bad CSV, duplicate orders, invalid amounts and unsafe totals fail explicitly', async t => {
  const context = await scratch(t);
  assert.throws(() => parseCSV('"unterminated'), /Unclosed/);
  assert.throws(() => parseCSV('"a"suffix,b'), /Unexpected/);
  for (const rows of [[order(), order()], [order({ quantity: 1.5 })], [order({ quantity: true })], [order({ unit_price_cents: -1 })], [order({ quantity: 2, unit_price_cents: Number.MAX_SAFE_INTEGER })]]) {
    await assert.rejects(sales({ rows }, context));
  }
  for (const csv_file of ['../secret', '/secret', 'sub/../secret', 'sub\\secret', 'sub//file']) await assert.rejects(sales({ csv_file }, context), /Invalid csv_file/);
  await fs.writeFile(path.join(context.input_dir, 'bad.csv'), 'order_id,region,quantity,unit_price_cents\none,north,1\n');
  await assert.rejects(sales({ csv_file: 'bad.csv' }, context), /column count/);
  await assert.rejects(sales({ rows: [order()], csv_file: 'bad.csv' }, context), /exclusively/);
});

test('report exports escape HTML and spreadsheet formulas', async t => {
  const context = await scratch(t);
  await sales({ rows: [order({ region: '=1+1<script>' })] }, context);
  assert.match(await fs.readFile(path.join(context.output_dir, 'summary.csv'), 'utf8'), /"'=1\+1<script>"/);
  const html = await fs.readFile(path.join(context.output_dir, 'report.html'), 'utf8');
  assert.match(html, /&lt;script&gt;/); assert.ok(!html.includes('<script>'));
});

async function audit(t, input) {
  const context = await scratch(t), inputPath = path.join(context.input_dir, 'input.json'), resultPath = path.join(context.output_dir, 'result.json');
  await fs.writeFile(inputPath, JSON.stringify(input));
  const child = spawnSync(process.env.PYTHON ?? 'python3', [path.join(root, 'scripts/python-handler.py'), path.join(root, 'invoice-audit/handler.py'), inputPath, context.output_dir, resultPath], { encoding: 'utf8' });
  if (child.error) throw child.error;
  assert.equal(child.status, 0, child.stderr);
  return { result: JSON.parse(await fs.readFile(resultPath)), records: JSON.parse(await fs.readFile(path.join(context.output_dir, 'audit.json'))).invoices,
    rejectedCSV: await fs.readFile(path.join(context.output_dir, 'rejected.csv'), 'utf8') };
}

test('Python audit separates accepted invoices from actionable errors', async t => {
  const { result, records } = await audit(t, await jsonFile('invoice-audit/input.json'));
  assert.deepEqual(result, await jsonFile('invoice-audit/expected.json'));
  assert.deepEqual(records.map(r => r.errors), [[], ['total_mismatch'], ['invalid_quantity']]);
});

test('invoice cents stay exact; duplicate IDs, bool quantities and non-decimal prices are rejected', async t => {
  const invoice = { invoice_id: 'exact', items: [{ quantity: 3, unit_price: '0.10' }], invoice_total: '0.30' };
  const { result, records, rejectedCSV } = await audit(t, { invoices: [invoice, invoice,
    { ...invoice, invoice_id: '=CMD()', items: [{ quantity: true, unit_price: '0.10' }] },
    { ...invoice, invoice_id: 'float', items: [{ quantity: 3, unit_price: 0.1 }] },
    { ...invoice, invoice_id: 'extra-decimal', invoice_total: '0.300' }] });
  assert.equal(result.accepted_total_cents, 30); assert.equal(result.accepted, 1);
  assert.deepEqual(records.slice(1).map(r => r.errors), [['duplicate_invoice_id'], ['invalid_quantity'], ['invalid_money'], ['invalid_money']]);
  assert.match(rejectedCSV, /'=CMD\(\)/);
});

test('parallel reports fan in through result objects and checksum-addressed CSV exports', async t => {
  const context = await scratch(t), input = {}, rows = (await jsonFile('sales-report/rows.json')).rows;
  for (const name of ['north', 'south']) {
    const branch = await scratch(t);
    input[name] = { status: 'succeeded', result: await sales({ rows: rows.filter(r => r.region === name) }, branch) };
    await fs.copyFile(path.join(branch.output_dir, 'summary.csv'), path.join(context.input_dir, `${name}.csv`));
  }
  assert.deepEqual(await merge(input, context), await jsonFile('report-pipeline/expected.json'));
  const metadata = JSON.parse(await fs.readFile(path.join(context.output_dir, 'combined.json')));
  for (const source of metadata.sources) assert.equal(source.sha256, createHash('sha256').update(await fs.readFile(path.join(context.input_dir, `${source.name}.csv`))).digest('hex'));
  await assert.rejects(merge({ ...input, south: { status: 'failed' } }, context), /Missing successful south/);
});

test('generated task manifests embed source, pin versions and declare contract/file handoffs', async () => {
  const examples = await buildExamples({ prefix: 'customer-demo', endpointID: '12345678-1234-1234-1234-123456789abc' });
  assert.deepEqual(Object.keys(examples.tasks), ['sales', 'audit', 'merge']);
  for (const task of Object.values(examples.tasks)) {
    assert.equal(task.version, 1); assert.ok(task.definition.source.length > 100);
    assert.equal(task.definition.artifact_storage, 'stored');
    assert.equal(task.definition.input_schema.type, 'object'); assert.equal(task.definition.result_schema.type, 'object');
    assert.deepEqual(task.definition.output_files.length, 2);
    assert.ok(task.definition.limits.timeout_ms <= 10000);
  }
  assert.equal(examples.tasks.audit.agent_endpoint_ids, undefined);
  assert.equal(examples.tasks.sales.agent_endpoint_ids.length, 1);
  const fanIn = examples.pipeline.steps.at(-1);
  assert.deepEqual(fanIn.depends_on, ['north', 'south']); assert.equal(fanIn.include_dependency_results, true);
  assert.deepEqual(fanIn.artifact_inputs.map(a => a.path), ['north.csv', 'south.csv']);
  assert.equal(examples.budgetHold.limits.max_invocations_per_day, 0);
  assert.equal(examples.budgetRelease.expected_revision, 1);
  assert.throws(() => names('../unsafe'));
  assert.throws(() => pipelineArguments(names('demo'), [order(), order({ region: 'unknown' })]), /north or south/);
});

test('recovery verifier distinguishes reused outputs from repeated physical work', () => {
  const branch = label => ({ label, outcome: 'succeeded', output_sha256: 'a'.repeat(64), attempts: [{ run_id: `${label}-run`, status: 'succeeded' }] });
  const parent = { completion_id: 'parent', outcome: 'failed', steps: [branch('north'), branch('south'), { label: 'merge', outcome: 'not_started', attempts: [] }] };
  const child = { completion_id: 'child', outcome: 'succeeded', rerun: { parent_id: 'parent' }, steps: parent.steps.map(s => s.label === 'merge'
    ? { label: 'merge', outcome: 'succeeded', attempts: [{ run_id: 'merge-run', status: 'succeeded' }] }
    : { ...s, reused_run_id: s.attempts[0].run_id, attempts: [] }) };
  const wrap = completion => ({ managed: [{ id: completion.completion_id, pipeline: { completion } }] });
  assert.deepEqual(checkRecovery(wrap(parent), wrap(child), 'parent', 'child'), { reused_reports: 2, new_attempts: 1, merge_run_id: 'merge-run' });
  const bad = structuredClone(child); bad.steps[0].attempts.push({ run_id: 'duplicate' });
  assert.throws(() => checkRecovery(wrap(parent), wrap(bad), 'parent', 'child'), /new attempt/);
  assert.throws(() => checkRecovery(wrap(parent), wrap(child), 'other', 'child'), /exact accepted/);
});
