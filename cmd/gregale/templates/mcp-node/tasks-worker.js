import { readFileSync } from 'node:fs';
import { startMcpTaskRuntime } from './task-runtime.js';

const config = JSON.parse(readFileSync(new URL('./gregale-mcp.json', import.meta.url)));
const taskServices = await startMcpTaskRuntime(config, { role: 'worker' });
console.log(JSON.stringify({ event: 'mcp_task_worker_started' }));

let shutdownPromise;
async function shutdown(signal) {
  if (shutdownPromise) return shutdownPromise;
  console.log(JSON.stringify({ event: 'mcp_task_worker_stopping', signal }));
  shutdownPromise = taskServices.close().catch(() => {
    process.exitCode = 1;
    console.error(JSON.stringify({ event: 'mcp_task_worker_shutdown_error' }));
  });
  return shutdownPromise;
}

process.once('SIGTERM', () => { void shutdown('SIGTERM'); });
process.once('SIGINT', () => { void shutdown('SIGINT'); });
