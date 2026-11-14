import { readFileSync } from 'node:fs';
import { startMcpTaskRuntime } from './task-runtime.js';
import { startMcpTaskWorkerReceipts } from './task-worker-receipts.js';

const config = JSON.parse(readFileSync(new URL('./gregale-mcp.json', import.meta.url)));
const taskServices = await startMcpTaskRuntime(config, { role: 'worker' });
const stopReceipts = startMcpTaskWorkerReceipts({ workerID: taskServices.taskRuntime.workerID, shutdownTimeoutMs: taskServices.shutdownTimeoutMs });

let shutdownPromise;
async function shutdown(signal) {
  if (shutdownPromise) return shutdownPromise;
  stopReceipts();
  console.log(JSON.stringify({ event: 'mcp_task_worker_stopping', signal }));
  shutdownPromise = taskServices.close().catch(() => {
    process.exitCode = 1;
    console.error(JSON.stringify({ event: 'mcp_task_worker_shutdown_error' }));
  });
  await shutdownPromise;
  process.exit(process.exitCode ?? 0);
}

process.once('SIGTERM', () => { void shutdown('SIGTERM').then(() => process.exit(process.exitCode || 0)); });
process.once('SIGINT', () => { void shutdown('SIGINT').then(() => process.exit(process.exitCode || 0)); });
