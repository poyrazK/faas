import { readFileSync } from 'node:fs';
import pg from 'pg';
import { resolveMcpTaskSettings } from './task-runtime.js';
import { mcpTaskHandlers } from './tasks.js';
import { createPostgresMcpTaskStore } from './task-store.js';
import { checkMcpTaskSchema } from './task-schema.js';

let pool;
try {
  const [action = 'apply', ...extra] = process.argv.slice(2);
  if (!['apply', 'status'].includes(action) || extra.length) throw new Error('Invalid migration command');
  const config = JSON.parse(readFileSync(new URL('./gregale-mcp.json', import.meta.url)));
  const env = { ...process.env };
  if (action === 'apply') {
    if (!env.MCP_TASK_MIGRATION_DATABASE_URL) throw new Error('A migration database binding is required');
    env[config.tasks.database_url_env] = env.MCP_TASK_MIGRATION_DATABASE_URL;
  }
  const settings = resolveMcpTaskSettings(config, { env, role: action === 'apply' ? 'worker' : 'observer' });
  pool = new pg.Pool({ connectionString: settings.databaseURL, max: 1, connectionTimeoutMillis: 5000, statement_timeout: 60000, application_name: 'gregale-mcp-tasks-migrate' });
  const report = action === 'apply'
    ? await createPostgresMcpTaskStore({ pool, ...settings }).migrate({ admissionHandlers: Object.entries(mcpTaskHandlers).map(([name, handler]) => ({ name, version: handler.version })) })
    : await checkMcpTaskSchema({ pool, namespace: settings.namespace, role: 'observer' });
  console.log(JSON.stringify({ ok: true, ...report }));
} catch (error) {
  const known = {
    'A migration database binding is required': 'Set MCP_TASK_MIGRATION_DATABASE_URL to the schema owner account',
    'MCP Task schema must not grant CREATE to PUBLIC': 'Use a dedicated trusted schema without CREATE privileges for PUBLIC',
    'Unsupported MCP Task schema version; migrations cannot downgrade it': 'The database schema is newer than this starter; use matching migration code',
  };
  console.log(JSON.stringify({ ok: false, detail: Object.hasOwn(known, error.message) ? known[error.message] : 'Task migration failed. Check the command, namespace, migration binding, schema ownership and encryption keys. Use a matching schema version; downgrade is unsupported.' }));
  process.exitCode = 1;
} finally { await pool?.end().catch(() => {}); }
