import { readFileSync } from 'node:fs';
import { createApp } from './app.js';
import { startProtocolHeartbeat } from './protocol-telemetry.js';
import { installOpenAPITools } from './openapi-tools.js';

const config = JSON.parse(readFileSync(new URL('./gregale-mcp.json', import.meta.url)));
const runtime = createApp(config);
installOpenAPITools(runtime);
const host = process.env.MCP_BIND_ADDRESS || (process.env.FAAS_APP_ID ? '0.0.0.0' : '127.0.0.1');
let stopHeartbeat = () => {};
const listener = runtime.app.listen(Number(process.env.PORT || 8080), host, () => {
  console.log(JSON.stringify({ event: 'mcp_listening', port: listener.address().port }));
  stopHeartbeat = startProtocolHeartbeat();
});
let stopping;
async function stop() {
  if (stopping) return;
  stopping = true;
  stopHeartbeat();
  runtime.beginDrain();
  const timeout = setTimeout(() => listener.closeAllConnections(), 10_000);
  await new Promise(resolve => listener.close(resolve));
  clearTimeout(timeout);
  runtime.closeTaskSubscriptions();
  await runtime.handler.close();
}
for (const signal of ['SIGTERM', 'SIGINT']) {
  process.once(signal, () => void stop().then(() => process.exit(0), () => process.exit(1)));
}
