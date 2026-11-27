import { startProtocolHeartbeat } from './protocol-telemetry.js';
import { readFileSync } from 'node:fs';
import { createApp } from './app.js';

const config = JSON.parse(readFileSync(new URL('./gregale-mcp.json', import.meta.url)));
const { app, handler, beginDrain, closeTaskSubscriptions } = createApp(config);
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
    let timeout;
    await new Promise(resolve => {
      listener.close(resolve);
      timeout = setTimeout(() => {
        closeTaskSubscriptions();
        listener.closeAllConnections();
      }, drainSeconds * 1000);
    });
    clearTimeout(timeout);
    closeTaskSubscriptions();
    await handler.close();
  })();
  return shutdownPromise;
}
function stop() { void shutdown().then(() => process.exit(0), () => process.exit(1)); }
process.once('SIGTERM', stop);
process.once('SIGINT', stop);
