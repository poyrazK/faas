import { readFileSync } from 'node:fs';
import { createApp } from './app.js';
import { startMcpTaskRuntime } from './task-runtime.js';

const config = JSON.parse(readFileSync(new URL('./gregale-mcp.json', import.meta.url)));
const role = process.env.MCP_TASKS_ROLE || 'combined';
if (!['combined', 'web'].includes(role)) throw new Error('Set MCP_TASKS_ROLE to combined or web; start the dedicated worker with npm run start:tasks-worker');
const taskServices = await startMcpTaskRuntime(config, { role });
const { taskRuntime } = taskServices;
const { app, handler, closeTaskSubscriptions } = createApp(config, { taskRuntime });
const appID = process.env.FAAS_APP_ID || '';
const host = process.env.MCP_BIND_ADDRESS || (appID ? '0.0.0.0' : '127.0.0.1');
const listener = app.listen(Number(process.env.PORT || 8080), host, () => console.log(JSON.stringify({ event: 'mcp_listening', port: listener.address().port })));
let stopping = false;
async function shutdown() {
  if (stopping) return;
  stopping = true;
  closeTaskSubscriptions();
  await new Promise(resolve => listener.close(resolve));
  await handler.close();
  await taskServices.close();
}
process.once('SIGTERM', () => { void shutdown(); });
process.once('SIGINT', () => { void shutdown(); });
