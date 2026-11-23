import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';

const exec = promisify(execFile);
const example = fileURLToPath(new URL('../examples/task-supervisor.mjs', import.meta.url));
const rawResult = '{"taskId":"original","status":"completed","result":{"structuredContent":{"counter":18446744073709551615}}}\n';
const invalidName = 'a\\invalid.json';

async function runSupervisor(t, mode) {
  const workspace = await mkdtemp(join(tmpdir(), 'gregale-supervisor-test-'));
  t.after(() => rm(workspace, { recursive: true, force: true }));
  const cli = join(workspace, 'gregale');
  await writeFile(cli, `#!/usr/bin/env node
const args = process.argv.slice(2);
const mode = process.env.SUPERVISOR_TEST_MODE;
const working = { taskId: 'original', status: 'working', nextAction: 'poll_status' };
const completed = { taskId: 'original', status: 'completed', nextAction: 'read_result' };
if (args.includes('--receipt-dir')) {
  const valid = { receipt: 'report.json', outcome: 'available', task: mode === 'lost-watch' ? working : completed };
  const inbox = mode === 'malformed-inbox' ? [{ receipt: ${JSON.stringify(invalidName)}, outcome: 'invalid_receipt' }, valid] : [valid];
  console.log(JSON.stringify(inbox));
} else if (args.includes('--read-result')) {
  process.stdout.write(${JSON.stringify(rawResult)});
} else if (args[2] === 'watch' && mode === 'lost-watch') {
  console.log(JSON.stringify(working));
  process.exitCode = 3;
} else {
  throw new Error('unexpected supervisor CLI command');
}
`, { mode: 0o700, flag: 'wx' });
  const { stdout, stderr } = await exec(process.execPath, [example, 'run', '--url', 'http://127.0.0.1:1/mcp',
    '--token-env', 'SUPERVISOR_TEST_TOKEN', '--dir', workspace], {
    env: { ...process.env, GREGALE_BIN: cli, SUPERVISOR_TEST_TOKEN: 'test-caller', SUPERVISOR_TEST_MODE: mode },
    timeout: 10_000,
  });
  assert.equal(stderr, '');
  return { records: stdout.trim().split('\n').filter(Boolean).map(line => JSON.parse(line)), workspace };
}

const saved = { receipt: 'report.json', action: 'result_saved', file: join('results', 'report.json') };

test('Task supervisor saves the original result JSON including large integers', async t => {
  const { records, workspace } = await runSupervisor(t, 'large-integer');
  assert.deepEqual(records, [saved]);
  assert.equal(await readFile(join(workspace, saved.file), 'utf8'), rawResult);
});

test('Task supervisor reports retry_status when a working Task loses its watch', async t => {
  const { records } = await runSupervisor(t, 'lost-watch');
  assert.deepEqual(records, [{ receipt: 'report.json', action: 'retry_status' }]);
});

test('Task supervisor keeps processing receipts after a malformed inbox filename', async t => {
  const { records } = await runSupervisor(t, 'malformed-inbox');
  assert.deepEqual(records, [{ receipt: invalidName, action: 'invalid_receipt' }, saved]);
});
