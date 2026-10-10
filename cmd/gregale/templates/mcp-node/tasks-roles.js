import { readFileSync } from 'node:fs';
import pg from 'pg';
import { resolveMcpTaskSettings } from './task-runtime.js';
import { planMcpTaskDatabaseGrants } from './task-schema.js';

let pool;
try {
  const [action, role, databaseRole, ...extra] = process.argv.slice(2);
  if (!['plan', 'grant'].includes(action) || extra.length) throw new Error('Invalid database role command');
  const config = JSON.parse(readFileSync(new URL('./gregale-mcp.json', import.meta.url)));
  const env = { ...process.env };
  if (action === 'grant') {
    if (!env.MCP_TASK_MIGRATION_DATABASE_URL) throw new Error('A migration database binding is required');
    env[config.tasks.database_url_env] = env.MCP_TASK_MIGRATION_DATABASE_URL;
  }
  const settings = resolveMcpTaskSettings(config, { env, role: 'observer' });
  pool = new pg.Pool({ connectionString: settings.databaseURL, max: 1, connectionTimeoutMillis: 5000, statement_timeout: 10000 });
  const plan = await planMcpTaskDatabaseGrants({ pool, role, databaseRole });
  if (action === 'grant') {
    const client = await pool.connect();
    try {
      await client.query('BEGIN');
      await client.query('SELECT pg_advisory_xact_lock(hashtextextended($1, 0))', ['gregale_mcp_tasks']);
      for (const statement of plan.statements) await client.query(statement);
      await client.query('COMMIT');
    } catch (error) { await client.query('ROLLBACK').catch(() => {}); throw error; }
    finally { client.release(); }
  }
  console.log(JSON.stringify({ ok: true, applied: action === 'grant', ...plan }));
} catch {
  console.log(JSON.stringify({ ok: false, detail: 'Task role operation failed. Check the existing database role, profile, schema, migration credentials and ownership.' }));
  process.exitCode = 1;
} finally { await pool?.end().catch(() => {}); }
