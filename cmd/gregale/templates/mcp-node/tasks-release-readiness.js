import { readFileSync } from 'node:fs';
import pg from 'pg';
import { resolveMcpTaskSettings } from './task-runtime.js';
import { mcpTaskHandlers } from './tasks.js';
import { checkMcpTaskReplacementReadiness } from './task-release.js';

let pool;
try {
  const config = JSON.parse(readFileSync(new URL('./gregale-mcp.json', import.meta.url)));
  const settings = resolveMcpTaskSettings(config, { role: 'observer' });
  const input = JSON.parse(process.env.MCP_TASK_RELEASE_INPUT);
  pool = new pg.Pool({ connectionString: settings.databaseURL, max: 1, connectionTimeoutMillis: 5000, statement_timeout: 5000 });
  const ok = await checkMcpTaskReplacementReadiness({ pool, namespace: settings.namespace, handlers: mcpTaskHandlers, workerIDs: input.replacementWorkerIDs });
  console.log(JSON.stringify({ ok }));
  if (!ok) process.exitCode = 1;
} catch {
  console.log(JSON.stringify({ ok: false }));
  process.exitCode = 1;
} finally { await pool?.end().catch(() => {}); }
