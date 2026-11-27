import { readFile } from 'node:fs/promises';
import { FaaSClient } from '@gregale/sdk-node/preview';
import { bindTask, type TaskResult } from './bindings.ts';

function requiredEnv(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`Set ${name} before submitting the report`);
  return value;
}
function decodeReport(value: unknown): TaskResult {
  const result = value as Partial<TaskResult> | null;
  if (!result || typeof result.words !== 'number' || !Number.isInteger(result.words) || result.words < 0 ||
      typeof result.pages !== 'number' || !Number.isInteger(result.pages) || result.pages < 0 ||
      typeof result.preview_truncated !== 'boolean') {
    throw new Error('Invalid PDF report result');
  }
  return { words: result.words, pages: result.pages, preview_truncated: result.preview_truncated };
}
const client = new FaaSClient(requiredEnv('FAAS_API'), { token: requiredEnv('FAAS_TOKEN') });
try {
  const task = bindTask(client, decodeReport);
  const report = await task.run({ title: 'Customer report' }, {
    idempotencyKey: requiredEnv('REPORT_REQUEST_ID'),
    files: { 'input.pdf': new Blob([new Uint8Array(await readFile(process.argv[2]!))]) },
    deliver: { app: requiredEnv('REPORT_APP'), route: requiredEnv('REPORT_ROUTE'), artifacts: { 'report.html': 'text/html' } },
  });
  console.log(JSON.stringify(report));
} finally { client.uninstall(); }
