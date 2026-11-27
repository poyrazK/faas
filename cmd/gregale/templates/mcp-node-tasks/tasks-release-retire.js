import { readFileSync } from 'node:fs';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { setTimeout as delay } from 'node:timers/promises';
import pg from 'pg';
import { resolveMcpTaskSettings } from './task-runtime.js';
import { mcpTaskHandlers } from './tasks.js';
import { restoreMcpTasks, quarantineMcpTaskWorkers } from './task-release.js';
let pool;
try {
  const [encoded] = process.argv.slice(2);
  const plan = JSON.parse(encoded);
  if (!Number.isSafeInteger(plan.timeoutMs) || plan.timeoutMs < 1000 || plan.timeoutMs > 600000) throw new Error('Invalid deadline');
  for (const hook of [plan.check, plan.park]) if (!Array.isArray(hook) || !hook.length || hook.some(arg => typeof arg !== 'string' || !arg || arg.includes('\0'))) throw new Error('Invalid hook');
  const config = JSON.parse(readFileSync(new URL('./gregale-mcp.json', import.meta.url)));
  const settings = resolveMcpTaskSettings(config, { role: 'worker' });
  pool = new pg.Pool({ connectionString: settings.databaseURL, max: 3, connectionTimeoutMillis: 5000, statement_timeout: 10000 });
  const env = { ...process.env, MCP_TASK_RELEASE_INPUT: JSON.stringify({ retirement: true }) };
  delete env.MCP_TASK_MIGRATION_DATABASE_URL;
  const run = hook => promisify(execFile)(hook[0], hook.slice(1), { env, timeout: plan.timeoutMs, killSignal: 'SIGKILL', maxBuffer: 65536 });
  const report = await restoreMcpTasks({ pool, namespace: settings.namespace, handlers: mcpTaskHandlers, restore: async () => {
    await run(plan.check);
    await quarantineMcpTaskWorkers({ pool, namespace: settings.namespace, workerIDs: plan.workerIDs });
    if (plan.retire) {
      await run(plan.park);
      const deadline = Date.now() + plan.timeoutMs;
      while ((await pool.query('SELECT worker_id FROM gregale_mcp_task_workers WHERE namespace=$1 AND worker_id=ANY($2::uuid[]) AND expires_at>clock_timestamp()', [settings.namespace, plan.workerIDs])).rows.length) {
        if (Date.now() >= deadline) throw new Error('Worker registrations did not disappear');
        await delay(250);
      }
      await run(plan.check);
      await pool.query('DELETE FROM gregale_mcp_task_workers WHERE namespace=$1 AND worker_id=ANY($2::uuid[]) AND draining', [settings.namespace, plan.workerIDs]);
    }
  } });
  report.stage = report.ok ? (plan.retire ? 'worker_retired' : 'quarantined') : report.stage;
  console.log(JSON.stringify(report));
  if (!report.ok) process.exitCode = 1;
} catch { console.log(JSON.stringify({ ok: false, stage: 'configuration' })); process.exitCode = 1; }
finally { await pool?.end().catch(() => {}); }
