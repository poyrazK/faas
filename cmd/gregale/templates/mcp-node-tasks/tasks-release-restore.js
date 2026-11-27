import { readFileSync } from 'node:fs';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import pg from 'pg';
import { resolveMcpTaskSettings } from './task-runtime.js';
import { mcpTaskHandlers } from './tasks.js';
import { restoreMcpTasks } from './task-release.js';
let pool;
try {
  const [encoded, deadline, ...extra] = process.argv.slice(2);
  const command = JSON.parse(encoded);
  const timeout = Number(deadline);
  if (!Number.isSafeInteger(timeout) || timeout < 1000 || timeout > 600000) throw new Error('Invalid deadline');
  if (extra.length || !Array.isArray(command) || !command.length || command.some(arg => typeof arg !== 'string' || !arg || arg.includes('\0'))) throw new Error('Invalid restore hook');
  const config = JSON.parse(readFileSync(new URL('./gregale-mcp.json', import.meta.url)));
  const settings = resolveMcpTaskSettings(config, { role: 'observer' });
  pool = new pg.Pool({ connectionString: settings.databaseURL, max: 2, connectionTimeoutMillis: 5000, statement_timeout: 10000 });
  const env = { ...process.env, MCP_TASK_RELEASE_INPUT: JSON.stringify({ restore: true }) };
  delete env.MCP_TASK_MIGRATION_DATABASE_URL;
  const report = await restoreMcpTasks({ pool, namespace: settings.namespace, handlers: mcpTaskHandlers,
    restore: () => promisify(execFile)(command[0], command.slice(1), { env, timeout, killSignal: 'SIGKILL', maxBuffer: 65536 }),
  });
  console.log(JSON.stringify(report));
  if (!report.ok) process.exitCode = 1;
} catch {
  console.log(JSON.stringify({ ok: false, stage: 'configuration' }));
  process.exitCode = 1;
} finally { await pool?.end().catch(() => {}); }
