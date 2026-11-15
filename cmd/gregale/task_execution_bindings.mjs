// Trusted CLI preflight. It reads metadata only and never changes schema/data.
import { createRequire } from 'node:module';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
const require = createRequire(resolve('package.json'));
const { Pool } = require('pg');
const pools = [];
try {
  const config = JSON.parse(readFileSync('gregale-mcp.json', 'utf8'));
  const runtime = process.env[config.tasks.database_url_env];
  const urls = [runtime, process.env.MCP_TASK_MIGRATION_DATABASE_URL, process.env.MCP_TASK_OBSERVER_DATABASE_URL];
  if (urls.some(value => !value)) throw new Error('binding');
  const facts = [];
  for (const url of urls) {
    const pool = new Pool({ connectionString: url, max: 1, connectionTimeoutMillis: 5000, statement_timeout: 5000 });
    pools.push(pool);
    const result = await pool.query(`SELECT current_database() AS database, current_schema() AS schema,
      inet_server_addr()::text AS server, inet_server_port() AS port, current_user AS role,
      r.rolsuper OR r.rolcreaterole OR r.rolcreatedb OR r.rolbypassrls AS privileged,
      n.nspowner::regrole::text AS owner,
      has_schema_privilege(current_user,n.oid,'CREATE') AS can_create
      FROM pg_roles r JOIN pg_namespace n ON n.nspname=current_schema() WHERE r.rolname=current_user`);
    if (result.rows.length !== 1) throw new Error('identity');
    facts.push(result.rows[0]);
  }
  const [r,m,o] = facts;
  if (!r.server || !r.port) throw new Error('TCP database identity required');
  for (const other of [m,o]) if (['database','schema','server','port','owner'].some(key => r[key] !== other[key])) throw new Error('different database');
  if (new Set(facts.map(f => f.role)).size !== 3 || r.privileged || o.privileged || m.privileged || r.can_create || o.can_create || m.role !== m.owner) throw new Error('role boundary');
  for (const [index, fact] of [[0,r],[2,o]]) {
    const member = await pools[index].query("SELECT pg_has_role(current_user,$1,'MEMBER') AS member",[m.role]);
    if (member.rows[0]?.member) throw new Error('owner membership');
    const privileges = await pools[index].query(`SELECT c.relname,
      has_table_privilege(current_user,c.oid,'SELECT') AS can_read,
      has_table_privilege(current_user,c.oid,'INSERT') AND has_table_privilege(current_user,c.oid,'UPDATE') AND
      has_table_privilege(current_user,c.oid,'DELETE') AS can_dml,
      has_table_privilege(current_user,c.oid,'INSERT') OR has_table_privilege(current_user,c.oid,'UPDATE') OR
      has_table_privilege(current_user,c.oid,'DELETE') OR has_table_privilege(current_user,c.oid,'TRUNCATE') OR
      has_table_privilege(current_user,c.oid,'TRIGGER') OR has_any_column_privilege(current_user,c.oid,'INSERT') OR
      has_any_column_privilege(current_user,c.oid,'UPDATE') AS can_write,
      has_table_privilege(current_user,c.oid,'TRUNCATE') OR has_table_privilege(current_user,c.oid,'TRIGGER') OR
      has_any_column_privilege(current_user,c.oid,'REFERENCES') AS can_administer,
      c.relowner::regrole::text AS owner FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
      WHERE n.nspname=current_schema() AND c.relkind IN ('r','p') AND c.relname ~ '^gregale_mcp_task'`);
    if (!privileges.rows.length || privileges.rows.some(row => row.owner !== m.role || row.can_administer || (index === 2 && row.can_write))) throw new Error('table privileges');
    const reads = index === 2 ? ['gregale_mcp_tasks','gregale_mcp_task_owners','gregale_mcp_task_workers','gregale_mcp_task_worker_handlers','gregale_mcp_task_schema','gregale_mcp_task_admission_namespaces'] : ['gregale_mcp_tasks','gregale_mcp_task_owners','gregale_mcp_task_fairness','gregale_mcp_task_workers','gregale_mcp_task_worker_handlers','gregale_mcp_task_schema','gregale_mcp_task_crypto_keys','gregale_mcp_task_admission','gregale_mcp_task_admission_namespaces'];
    if (reads.some(name => !privileges.rows.some(row => row.relname === name && row.can_read))) throw new Error('read privileges');
    const writes = ['gregale_mcp_tasks','gregale_mcp_task_owners','gregale_mcp_task_fairness','gregale_mcp_task_workers','gregale_mcp_task_worker_handlers'];
    if (index === 0 && (writes.some(name => !privileges.rows.some(row => row.relname === name && row.can_dml)) || privileges.rows.some(row => !writes.includes(row.relname) && row.can_write))) throw new Error('runtime privileges');
    void fact;
  }
  process.stdout.write('{"verified":true}');
} catch {
  process.exitCode = 1;
} finally {
  await Promise.all(pools.map(pool => pool.end().catch(() => {})));
}
