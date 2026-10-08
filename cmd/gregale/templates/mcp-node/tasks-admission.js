import { readFileSync } from 'node:fs';
import pg from 'pg';
import { resolveMcpTaskSettings } from './task-runtime.js';
import { createMcpTaskAdmissionController } from './task-admission.js';

let pool;
try {
  const [action = 'status', name, version, ...extra] = process.argv.slice(2);
  if (!['status', 'audit', 'allow', 'disable', 'retire'].includes(action) || extra.length ||
      (['status', 'audit'].includes(action) ? name !== undefined || version !== undefined : !name || !version)) throw new Error('Invalid admission command');
  const config = JSON.parse(readFileSync(new URL('./gregale-mcp.json', import.meta.url)));
  const settings = resolveMcpTaskSettings(config, { role: 'observer' });
  pool = new pg.Pool({ connectionString: settings.databaseURL, max: 1, connectionTimeoutMillis: 5000, statement_timeout: 10000 });
  const controller = createMcpTaskAdmissionController({ pool, namespace: settings.namespace });
  const result = action === 'status' ? await controller.status() : action === 'audit' ? await controller.audit() : await controller.change(action, name, version);
  console.log(JSON.stringify({ ok: true, ...result }));
} catch {
  console.log(JSON.stringify({ ok: false, detail: 'Task admission operation failed. Check command arguments, database/namespace bindings and operator privileges. Retirement requires disabled admission and no retained nonterminal Tasks.' }));
  process.exitCode = 1;
} finally { await pool?.end().catch(() => {}); }
