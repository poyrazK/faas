// The MCP server with durable MCP Tasks. app-core.js is the server the
// plain mcp-node starter ships as its app.js; this file adds the Tasks module.
import * as tasks from './tasks.js';
import { createApp as createCoreApp } from './app-core.js';

export function createApp(config, options = {}) {
  return createCoreApp(config, { tasks, ...options });
}
