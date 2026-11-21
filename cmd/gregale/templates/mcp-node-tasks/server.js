import { startProtocolHeartbeat } from './protocol-telemetry.js';
import { readFileSync } from 'node:fs';
import { createApp } from './app.js';
import { startMcpTaskRuntime } from './task-runtime.js';

const config = JSON.parse(readFileSync(new URL('./gregale-mcp.json', import.meta.url)));
const role = process.env.MCP_TASKS_ROLE || 'combined';
if (!['combined', 'web'].includes(role)) throw new Error('Set MCP_TASKS_ROLE to combined or web; start the dedicated worker with npm run start:tasks-worker');
const taskServices = await startMcpTaskRuntime(config, { role });
const { taskRuntime } = taskServices;
const { app, handler, beginDrain, closeTaskSubscriptions } = createApp(config, { taskRuntime });
const appID = process.env.FAAS_APP_ID || '';
const host = process.env.MCP_BIND_ADDRESS || (appID ? '0.0.0.0' : '127.0.0.1');
let stopHeartbeat = () => {};
const listener = app.listen(Number(process.env.PORT || 8080), host, () => { console.log(JSON.stringify({ event: 'mcp_listening', port: listener.address().port })); stopHeartbeat = startProtocolHeartbeat(); });
const drainSeconds = Number(process.env.MCP_DRAIN_TIMEOUT_SECONDS ?? 10);
if (!Number.isInteger(drainSeconds) || drainSeconds < 1 || drainSeconds > 300) throw new Error('MCP_DRAIN_TIMEOUT_SECONDS must be an integer between 1 and 300');
let shutdownPromise;
async function shutdown() {
  if (shutdownPromise) return shutdownPromise;
  stopHeartbeat();
  beginDrain();
  shutdownPromise = (async () => {
    const taskDrain = taskServices.close();
    let timeout;
    const webDrain = new Promise(resolve => {
      listener.close(resolve);
      timeout = setTimeout(() => {
        closeTaskSubscriptions();
        listener.closeAllConnections();
      }, drainSeconds * 1000);
    });
    await Promise.all([webDrain, taskDrain]);
    clearTimeout(timeout);
    closeTaskSubscriptions();
    await handler.close();
  })();
  return shutdownPromise;
}
function stop() { void shutdown().then(() => process.exit(0), () => process.exit(1)); }
process.once('SIGTERM', stop);
process.once('SIGINT', stop);
