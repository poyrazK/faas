import { readFileSync } from 'node:fs';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import pg from 'pg';
import { resolveMcpTaskSettings } from './task-runtime.js';
import { createPostgresMcpTaskStore } from './task-store.js';
import { mcpTaskHandlers } from './tasks.js';
import { releaseMcpTasks } from './task-release.js';

let pool, migrationPool;
try {
  const [planPath, ...extra] = process.argv.slice(2);
  if (!planPath || extra.length) throw new Error('Release plan required');
  const plan = JSON.parse(readFileSync(planPath, 'utf8'));
  const timeoutMs = plan.timeoutMs ?? 60000;
  if (!Number.isSafeInteger(timeoutMs) || timeoutMs < 1000 || timeoutMs > 600000) throw new Error('Invalid deadline');
  for (const hook of [plan.start, plan.drain]) if (!Array.isArray(hook) || !hook.length || hook.some(arg => typeof arg !== 'string' || !arg || arg.includes('\0'))) throw new Error('Invalid hook');
  if (!process.env.MCP_TASK_MIGRATION_DATABASE_URL) throw new Error('Migration binding required');
  const config = JSON.parse(readFileSync(new URL('./gregale-mcp.json', import.meta.url)));
  const settings = resolveMcpTaskSettings(config, { role: 'worker' });
  pool = new pg.Pool({ connectionString: settings.databaseURL, max: 4, connectionTimeoutMillis: 5000, statement_timeout: 10000 });
  migrationPool = new pg.Pool({ connectionString: process.env.MCP_TASK_MIGRATION_DATABASE_URL, max: 1, connectionTimeoutMillis: 5000, statement_timeout: 60000 });
  const run = async (command, input) => {
    const env = { ...process.env, MCP_TASK_RELEASE_INPUT: JSON.stringify(input) };
    delete env.MCP_TASK_MIGRATION_DATABASE_URL;
    const { stdout } = await promisify(execFile)(command[0], command.slice(1), { env, timeout: timeoutMs, killSignal: 'SIGKILL', maxBuffer: 65536 });
    return stdout;
  };
  const report = await releaseMcpTasks({ pool, namespace: settings.namespace, handlers: mcpTaskHandlers,
    store: createPostgresMcpTaskStore({ pool, ...settings }), timeoutMs,
    migrate: () => createPostgresMcpTaskStore({ pool: migrationPool, ...settings }).migrate({ admissionHandlers: Object.entries(mcpTaskHandlers).map(([name, handler]) => ({ name, version: handler.version })) }),
    start: async input => JSON.parse(await run(plan.start, input)).workerIDs,
    drain: input => run(plan.drain, input),
  });
  console.log(JSON.stringify(report));
  if (!report.ok) process.exitCode = 1;
} catch {
  console.log(JSON.stringify({ ok: false, stage: 'configuration', detail: 'Check release plan, Task bindings, and migration credentials' }));
  process.exitCode = 1;
} finally {
  await pool?.end().catch(() => {});
  await migrationPool?.end().catch(() => {});
}
