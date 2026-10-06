import { readFileSync } from 'node:fs';
import { createApp } from './app.js';

const config = JSON.parse(readFileSync(new URL('./gregale-mcp.json', import.meta.url)));
const { app, handler } = createApp(config);
const appID = process.env.FAAS_APP_ID || '';
const host = process.env.MCP_BIND_ADDRESS || (appID ? '0.0.0.0' : '127.0.0.1');
const listener = app.listen(Number(process.env.PORT || 8080), host, () => console.log(JSON.stringify({ event: 'mcp_listening', port: listener.address().port })));
process.on('SIGTERM', () => { listener.close(); void handler.close(); });
