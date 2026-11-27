import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { parseArgs } from 'node:util';

export const root = fileURLToPath(new URL('../', import.meta.url));
export const jsonFile = async name => JSON.parse(await fs.readFile(path.join(root, name), 'utf8'));
export function names(prefix) {
  if (!/^[a-z][a-z0-9-]{0,29}$/.test(prefix)) throw new Error('prefix must be 1..30 lowercase letters, digits or hyphens, starting with a letter');
  return { sales: `${prefix}-sales`, audit: `${prefix}-audit`, merge: `${prefix}-merge` };
}
export function pipelineArguments(taskNames, rows) {
  if (!Array.isArray(rows) || rows.some(row => !['north', 'south'].includes(row?.region))) throw new Error('Every order must belong to north or south');
  const steps = ['north', 'south'].map(region => ({
    label: region,
    task: { name: taskNames.sales, version: 1, input: { rows: rows.filter(row => row.region === region) } },
  }));
  if (steps.some(step => step.task.input.rows.length === 0)) throw new Error('The example needs at least one order in each of north and south');
  steps.push({ label: 'merge', task: { name: taskNames.merge, version: 1 }, depends_on: ['north', 'south'], include_dependency_results: true,
    artifact_inputs: ['north', 'south'].map(region => ({ from_step: region, name: 'summary.csv', path: `${region}.csv` })) });
  return { pipeline: { total_timeout_ms: 120000 }, max_parallel_steps: 2, steps };
}
export async function buildExamples({ prefix = 'gr-demo', endpointID } = {}) {
  const taskNames = names(prefix), rows = (await jsonFile('sales-report/rows.json')).rows;
  if (endpointID && !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(endpointID)) throw new Error('endpoint-id must be a UUID');
  const tasks = {};
  for (const [kind, directory, filename, outputFiles, runtime] of [
    ['sales', 'sales-report', 'handler.mjs', ['summary.csv', 'report.html'], 'node24'],
    ['audit', 'invoice-audit', 'handler.py', ['audit.json', 'rejected.csv'], 'python313'],
    ['merge', 'report-pipeline', 'handler.mjs', ['combined.json', 'combined.html'], 'node24'],
  ]) {
    tasks[kind] = { name: taskNames[kind], version: 1,
      ...(endpointID && kind !== 'audit' ? { agent_endpoint_ids: [endpointID] } : {}),
      definition: { runtime, source: await fs.readFile(path.join(root, directory, filename), 'utf8'),
        input_schema: await jsonFile(`${directory}/input.schema.json`), result_schema: await jsonFile(`${directory}/result.schema.json`),
        artifact_storage: 'stored', output_files: outputFiles,
        limits: { timeout_ms: 5000, memory_mb: 128, max_output_bytes: 65536, max_artifact_bytes: 32768 } } };
  }
  return { tasks, pipeline: { workflow_id: `${prefix}-reports`, version: 'v1', ...pipelineArguments(taskNames, rows) },
    agentArguments: pipelineArguments(taskNames, rows),
    budgetHold: { expected_revision: 0, enabled: true, limits: { max_invocations_per_day: 0 } },
    budgetRelease: { expected_revision: 1, enabled: true, limits: { max_invocations_per_day: 1 } } };
}
export async function writeExamples(directory, options) {
  const examples = await buildExamples(options);
  await fs.mkdir(directory, { recursive: true });
  for (const [kind, task] of Object.entries(examples.tasks)) await fs.writeFile(path.join(directory, `${kind}.task.json`), JSON.stringify(task, null, 2) + '\n');
  for (const [name, body] of [['pipeline.json', examples.pipeline], ['agent-arguments.json', examples.agentArguments], ['budget-hold.json', examples.budgetHold], ['budget-release.json', examples.budgetRelease]]) {
    await fs.writeFile(path.join(directory, name), JSON.stringify(body, null, 2) + '\n');
  }
  return examples;
}
if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  const { values } = parseArgs({ options: { prefix: { type: 'string', default: 'gr-demo' }, out: { type: 'string', default: 'generated' }, 'endpoint-id': { type: 'string' } } });
  await writeExamples(path.resolve(values.out), { prefix: values.prefix, endpointID: values['endpoint-id'] });
  console.log(`Wrote task, pipeline, Agent and budget manifests to ${path.resolve(values.out)}`);
}
