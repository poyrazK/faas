// A restartable supervisor for the starter's build_report approval Task.
import { execFile, spawn } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { constants } from 'node:fs';
import { link, lstat, mkdir, open, unlink } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { createInterface } from 'node:readline';
import { parseArgs, promisify } from 'node:util';

const exec = promisify(execFile);
const maxJSONBytes = 8 * 1024 * 1024;
const cli = process.env.GREGALE_BIN || 'gregale';
const emit = record => process.stdout.write(JSON.stringify(record) + '\n');

function checkReceiptName(name) {
  if (typeof name !== 'string' || !name || name === '.' || name === '..' || /[\\/\0]/.test(name) ||
      name.startsWith('.gregale-submission-')) {
    throw new Error('a receipt name must be a single filename');
  }
  return name;
}

async function privateDirectory(path) {
  await mkdir(path, { recursive: true, mode: 0o700 });
  const state = await lstat(path);
  if (!state.isDirectory() || state.mode & 0o077) {
    throw new Error('the supervisor workspace and its subdirectories must be private directories');
  }
}

function parseJSON(text) {
  try {
    return JSON.parse(text);
  } catch {
    throw new Error('invalid JSON from a supervisor file or Gregale');
  }
}

async function readPrivateJSON(path) {
  let file;
  try {
    file = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
  } catch (error) {
    if (error.code === 'ENOENT') return undefined;
    throw new Error('could not open the private supervisor file');
  }
  try {
    const state = await file.stat();
    if (!state.isFile() || state.mode & 0o077 || state.size > maxJSONBytes) {
      throw new Error('supervisor files must be private, regular JSON files within 8 MiB');
    }
    const buffer = Buffer.alloc(state.size + 1);
    let offset = 0;
    while (offset < buffer.length) {
      const { bytesRead } = await file.read(buffer, offset, buffer.length - offset, offset);
      if (bytesRead === 0) break;
      offset += bytesRead;
    }
    if (offset !== state.size) throw new Error('supervisor file changed during the read');
    return parseJSON(buffer.subarray(0, offset));
  } finally {
    await file.close();
  }
}

async function saveResult(directory, filename, raw) {
  const temporary = join(directory, `.result-${randomUUID()}`);
  const file = await open(temporary, 'wx', 0o600);
  try {
    try {
      await file.writeFile(raw);
      await file.sync();
    } finally {
      await file.close();
    }
    try {
      await link(temporary, join(directory, filename));
    } catch (error) {
      if (error.code === 'EEXIST') return false;
      throw error;
    }
    const parent = await open(directory, 'r');
    try {
      await parent.sync();
    } finally {
      await parent.close();
    }
    return true;
  } finally {
    await unlink(temporary);
  }
}

function invocation(options, verb, args) {
  return ['mcp', 'client', verb, ...options.connection, '--json', ...args];
}

async function request(options, verb, args, { allowUnconfirmed = false } = {}) {
  let stdout;
  try {
    ({ stdout } = await exec(cli, invocation(options, verb, args), { maxBuffer: maxJSONBytes }));
  } catch (error) {
    if ([1, 3].includes(error.code)) {
      if (error.stdout?.trim()) return { code: error.code, value: parseJSON(error.stdout) };
      if (allowUnconfirmed) return { code: error.code };
    }
    throw new Error(`gregale mcp client ${verb} did not return an outcome`);
  }
  return { value: parseJSON(stdout), code: 0, raw: stdout };
}

function matchesResult(value, taskID) {
  return value?.taskId === taskID && value.status === 'completed' &&
    value.result != null && typeof value.result === 'object' && !Array.isArray(value.result);
}

async function readResult(options, receipt, task, selector) {
  const destination = join(options.results, receipt);
  const file = join('results', receipt);
  const saved = await readPrivateJSON(destination);
  if (saved !== undefined) {
    if (!matchesResult(saved, task.taskId)) throw new Error('saved result belongs to a different Task or is invalid');
    emit({ receipt, action: 'already_saved', file });
    return;
  }
  const { code, value, raw } = await request(options, 'status', [...selector, '--read-result']);
  if (code === 3) {
    emit({ receipt, action: 'retry_status' });
    return;
  }
  if (code !== 0 || !matchesResult(value, task.taskId)) throw new Error('the completed Task result was unavailable');
  const created = await saveResult(options.results, receipt, raw);
  if (!created && !matchesResult(await readPrivateJSON(destination), task.taskId)) {
    throw new Error('concurrent result belongs to a different Task or is invalid');
  }
  emit({ receipt, action: created ? 'result_saved' : 'already_saved', file });
}

async function handleStatus(options, receipt, task, answer) {
  const selector = ['--task-receipt', join(options.receipts, receipt)];
  if (task.nextAction === 'provide_input') {
    const reviewPath = join(options.reviews, receipt);
    const existing = await readPrivateJSON(reviewPath);
    if (existing === undefined) {
      const { code } = await request(options, 'status', [...selector, '--write-input-template', reviewPath]);
      if (code !== 0) { emit({ receipt, action: 'retry_status' }); return false; }
    } else if (existing?.taskId !== task.taskId) {
      throw new Error('review file belongs to a different Task');
    } else if (answer) {
      const forms = Object.values(existing.forms ?? {});
      if (forms.length > 0 && forms.every(form => form?.response != null)) {
        const { code, value } = await request(options, 'respond', [...selector, '--reviewed-input-file', reviewPath]);
        if (code === 3) { emit({ receipt, action: value.nextAction }); return false; }
        return handleStatus(options, receipt, value, false);
      }
    }
    emit({ receipt, action: 'review_required', file: join('reviews', receipt) });
  } else if (task.nextAction === 'read_result') {
    await readResult(options, receipt, task, selector);
  } else if (task.nextAction !== 'poll_status') {
    emit({ receipt, action: task.nextAction, status: task.status });
  }
  return task.nextAction === 'poll_status';
}

async function observe(options, receipt, record, answer) {
  if (record.outcome && record.outcome !== 'available') {
    emit({ receipt, action: record.task?.nextAction ?? record.outcome });
    return false;
  }
  try {
    return await handleStatus(options, receipt, record.task ?? record, answer);
  } catch (error) {
    emit({ receipt, action: 'error', message: error.message });
    return false;
  }
}

async function watch(options, names) {
  if (!names.length) return;
  const selectors = names.flatMap(name => ['--task-receipt', join(options.receipts, name)]);
  const child = spawn(cli, invocation(options, 'watch', selectors), { stdio: ['ignore', 'pipe', 'ignore'] });
  const exited = new Promise(resolve => {
    child.once('error', () => resolve(null));
    child.once('close', resolve);
  });
  let interrupted = false;
  const stop = () => {
    interrupted = true;
    child.kill('SIGINT');
  };
  process.once('SIGINT', stop);
  process.once('SIGTERM', stop);
  const pending = new Set(names);
  let received = false;
  try {
    for await (const line of createInterface({ input: child.stdout, crlfDelay: Infinity })) {
      const event = parseJSON(line);
      const name = names[names.length === 1 ? 0 : event.receiptIndex];
      if (!name) throw new Error('watch returned an unknown receipt position');
      received = true;
      if (!await observe(options, name, event, false)) pending.delete(name);
    }
    const code = await exited;
    if (interrupted) return;
    if (code !== 0 && (!received || ![1, 3].includes(code))) {
      throw new Error('gregale mcp client watch ended without a status outcome');
    }
    for (const receipt of pending) emit({ receipt, action: 'retry_status' });
  } finally {
    process.off('SIGINT', stop);
    process.off('SIGTERM', stop);
    if (child.exitCode === null && child.signalCode === null) stop();
  }
}

async function jobArguments(path) {
  const filename = resolve(path);
  const state = await lstat(filename).catch(() => undefined);
  if (!state?.isFile() || state.mode & 0o077) {
    throw new Error('the job file must be a private, regular JSON file');
  }
  return ['--arguments-file', filename];
}

function reportCall(selector, argumentsFlag) {
  return ['--tasks', '--defer-form-input', '--tool', 'build_report', ...selector, ...argumentsFlag];
}

async function recover(options, receipt, argumentsFlag) {
  const selector = ['--task-receipt', join(options.receipts, receipt)];
  const retry = await request(options, 'call', [...reportCall(selector, argumentsFlag), '--retry-submission'],
    { allowUnconfirmed: true });
  const taskID = retry.value?.result?.taskId;
  if (retry.code === 0 && !taskID) throw new Error('submission retry did not return a Task handle');

  // A lost retry reply and a closed submission window can both leave a
  // retained Task. Read just the selected receipt before advancing it.
  const { code, value: task } = await request(options, 'status', [...selector, '--metadata-only'],
    { allowUnconfirmed: true });
  if (code !== 0) {
    emit({ receipt, action: code === 3 || retry.code === 0 ? 'retry_status' : 'submission_unresolved' });
    return;
  }
  if (!task?.taskId || (retry.code === 0 && task.taskId !== taskID)) {
    throw new Error('receipt status does not match the recovered Task');
  }
  if (await observe(options, receipt, task, false)) await watch(options, [receipt]);
}

async function main() {
  const { values, positionals } = parseArgs({
    allowPositionals: true,
    options: {
      url: { type: 'string' }, app: { type: 'string' }, 'token-env': { type: 'string' },
      dir: { type: 'string' }, receipt: { type: 'string' }, report: { type: 'string' }, 'job-file': { type: 'string' },
    },
  });
  const [command] = positionals;
  if (positionals.length !== 1 || !['submit', 'recover', 'run'].includes(command) ||
      Boolean(values.url) === Boolean(values.app) || !values.dir || !values['token-env'] ||
      !process.env[values['token-env']]) {
    throw new Error('use submit, recover or run with --url or --app, --dir, and a populated --token-env');
  }
  const directory = resolve(values.dir);
  const options = {
    connection: [values.url ? '--url' : '--app', values.url || values.app, '--token-env', values['token-env']],
    receipts: join(directory, 'receipts'), reviews: join(directory, 'reviews'), results: join(directory, 'results'),
  };
  for (const path of [directory, options.receipts, options.reviews, options.results]) await privateDirectory(path);

  if (command === 'submit') {
    const receipt = checkReceiptName(values.receipt);
    if (Boolean(values.report) === Boolean(values['job-file'])) {
      throw new Error('submit requires --receipt and either --report or --job-file');
    }
    const argumentsFlag = values['job-file'] ? await jobArguments(values['job-file']) :
      ['--arguments', JSON.stringify({ report: values.report, steps: 1, request_approval: true })];
    const selector = ['--task-receipt', join(options.receipts, receipt)];
    const { code, value } = await request(options, 'call', reportCall(selector, argumentsFlag), { allowUnconfirmed: true });
    if (code !== 0 || !value.result?.taskId) throw new Error('submission outcome was unavailable; use recover with the original job file');
    emit({ receipt, action: 'submitted', taskId: value.result.taskId });
    return;
  }
  if (command === 'recover') {
    if (values.report || !values['job-file']) throw new Error('recover requires --receipt and --job-file');
    const receipt = checkReceiptName(values.receipt);
    await recover(options, receipt, await jobArguments(values['job-file']));
    return;
  }
  if (values.report || values.receipt || values['job-file']) {
    throw new Error('run uses the workspace receipt directory; use recover to select one receipt');
  }
  const { value: inbox } = await request(options, 'status', ['--metadata-only', '--receipt-dir', options.receipts]);
  if (!Array.isArray(inbox)) throw new Error('receipt directory did not return an inbox');
  const working = [];
  for (const item of inbox) {
    if (item.outcome === 'invalid_receipt') {
      emit({ receipt: item.receipt, action: 'invalid_receipt' });
      continue;
    }
    const receipt = checkReceiptName(item.receipt);
    if (await observe(options, receipt, item, true)) working.push(receipt);
  }
  await watch(options, working);
}

main().catch(error => {
  process.stderr.write(error.message + '\n');
  process.exitCode = 1;
});
