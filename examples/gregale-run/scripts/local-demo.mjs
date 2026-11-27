import fs from 'node:fs/promises';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { parseArgs } from 'node:util';
import sales from '../sales-report/handler.mjs';
import merge from '../report-pipeline/handler.mjs';
import { root, jsonFile, names, pipelineArguments } from './manifests.mjs';

const { values } = parseArgs({ options: { out: { type: 'string', default: '.output/local' } } });
const output = path.resolve(values.out);
await fs.mkdir(output, { recursive: true });
const context = async (name, inputDir = path.join(root, 'sales-report')) => {
  const dir = path.join(output, name); await fs.mkdir(dir, { recursive: true });
  return { execution_id: `local-${name}`, input_dir: inputDir, output_dir: dir };
};
const report = await sales(await jsonFile('sales-report/input.json'), await context('sales-report'));
const auditContext = await context('invoice-audit');
const resultFile = path.join(auditContext.output_dir, 'result.json');
const child = spawnSync(process.env.PYTHON ?? 'python3', [path.join(root, 'scripts/python-handler.py'), path.join(root, 'invoice-audit/handler.py'), path.join(root, 'invoice-audit/input.json'), auditContext.output_dir, resultFile], { encoding: 'utf8' });
if (child.error || child.status !== 0) throw child.error ?? new Error(child.stderr || 'Python handler failed');
const audit = JSON.parse(await fs.readFile(resultFile, 'utf8'));
const rows = (await jsonFile('sales-report/rows.json')).rows;
const dependencies = {}, mergeContext = await context('pipeline-merge', path.join(output, 'pipeline-inputs'));
await fs.mkdir(mergeContext.input_dir, { recursive: true });
for (const region of ['north', 'south']) {
  const c = await context(`pipeline-${region}`);
  dependencies[region] = { status: 'succeeded', result: await sales({ rows: rows.filter(row => row.region === region) }, c) };
  await fs.copyFile(path.join(c.output_dir, 'summary.csv'), path.join(mergeContext.input_dir, `${region}.csv`));
}
const combined = await merge(dependencies, mergeContext);
await fs.writeFile(path.join(output, 'pipeline-input.json'), JSON.stringify(dependencies, null, 2) + '\n');
const results = { sales: report, invoices: audit, pipeline: combined };
await fs.writeFile(path.join(output, 'results.json'), JSON.stringify(results, null, 2) + '\n');
console.log(JSON.stringify({ ...results, artifact_directory: output, agent_tool_input: pipelineArguments(names('gr-demo'), rows) }, null, 2));
console.log('Local handler demonstration only. No Gregale credentials, VM, API call, admission budget or durable inbox was used.');
