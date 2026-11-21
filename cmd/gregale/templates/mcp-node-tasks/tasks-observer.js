import fs from 'node:fs';
import { startMcpTaskRuntime } from './task-runtime.js';

const config = JSON.parse(fs.readFileSync('gregale-mcp.json', 'utf8'));
const service = await startMcpTaskRuntime(config, { role: 'observer' });
for (const signal of ['SIGINT', 'SIGTERM']) {
  process.once(signal, () => { void service.close().then(() => process.exit(0)); });
}
