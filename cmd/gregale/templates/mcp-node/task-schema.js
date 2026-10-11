export const MCP_TASK_SCHEMA_VERSION = 1;

export const MCP_TASK_DATABASE_PROFILES = Object.freeze({
  runtime: {
    gregale_mcp_tasks: ['SELECT', 'INSERT', 'UPDATE', 'DELETE'],
    gregale_mcp_task_fairness: ['SELECT', 'INSERT', 'UPDATE', 'DELETE'],
    gregale_mcp_task_workers: ['SELECT', 'INSERT', 'UPDATE', 'DELETE'],
    gregale_mcp_task_crypto_keys: ['SELECT'],
    gregale_mcp_task_admission: ['SELECT'],
    gregale_mcp_task_admission_namespaces: ['SELECT'],
    gregale_mcp_task_schema: ['SELECT'],
  },
  observer: {
    gregale_mcp_tasks: ['SELECT'], gregale_mcp_task_workers: ['SELECT'],
    gregale_mcp_task_schema: ['SELECT'], gregale_mcp_task_admission_namespaces: ['SELECT'],
  },
  operator: {
    gregale_mcp_tasks: ['SELECT'], gregale_mcp_task_admission: ['SELECT', 'INSERT', 'UPDATE'],
    gregale_mcp_task_admission_namespaces: ['SELECT'], gregale_mcp_task_admission_audit: ['SELECT'],
    gregale_mcp_task_schema: ['SELECT'],
  },
});

export async function assertMcpTaskSchemaTrust(pool) {
  const result = await pool.query(`SELECT NOT EXISTS (
    SELECT 1 FROM pg_namespace CROSS JOIN LATERAL aclexplode(COALESCE(nspacl, acldefault('n', nspowner))) AS acl
    WHERE nspname = current_schema() AND acl.grantee = 0 AND acl.privilege_type = 'CREATE'
  ) AS trusted`);
  if (!result.rows[0]?.trusted) throw new Error('MCP Task schema must not grant CREATE to PUBLIC');
}

export async function checkMcpTaskSchema({ pool, namespace, role = 'runtime', inTransaction = false }) {
  const privileges = Object.hasOwn(MCP_TASK_DATABASE_PROFILES, role) ? MCP_TASK_DATABASE_PROFILES[role] : undefined;
  if (!pool || typeof pool.query !== 'function' || !privileges || typeof namespace !== 'string' || !namespace.trim()) throw new Error('Invalid MCP Task schema settings');
  if (!inTransaction) {
    const client = typeof pool.connect === 'function' ? await pool.connect() : pool;
    try {
      await client.query('BEGIN READ ONLY');
      const checked = await checkMcpTaskSchema({ pool: client, namespace, role, inTransaction: true });
      await client.query('COMMIT');
      return checked;
    } catch (error) { await client.query('ROLLBACK').catch(() => {}); throw error; }
    finally { if (client !== pool) client.release(); }
  }
  await pool.query('SELECT pg_advisory_xact_lock_shared(hashtextextended($1, 0))', ['gregale_mcp_tasks']);
  const result = await pool.query('SELECT version FROM gregale_mcp_task_schema WHERE singleton = true');
  if (result.rows.length !== 1 || result.rows[0].version !== MCP_TASK_SCHEMA_VERSION) throw new Error('Unsupported MCP Task schema version; run the matching tasks:migrate command before startup');
  await assertMcpTaskSchemaTrust(pool);
  for (const [table, required] of Object.entries(privileges)) {
    for (const permission of required) {
      const allowed = await pool.query('SELECT has_table_privilege(current_user, $1, $2) AS allowed', [table, permission]);
      if (!allowed.rows[0]?.allowed) throw new Error(`MCP Task ${role} database privileges are incomplete`);
    }
  }
  if (role === 'runtime') {
    const allowed = await pool.query("SELECT has_sequence_privilege(current_user, 'gregale_mcp_task_claim_order_seq', 'USAGE') AS allowed");
    if (!allowed.rows[0]?.allowed) throw new Error('MCP Task runtime sequence privileges are incomplete');
  }
  await pool.query('SELECT namespace, task_id, owner_hash, tool_name, handler_version, arguments_encrypted, result_encrypted, error_encrypted, input_state_encrypted, status, created_at, updated_at, expires_at, attempt_count, resume_pending, next_attempt_at, lease_token, lease_expires_at, cancel_requested_at, input_methods FROM gregale_mcp_tasks LIMIT 0');
  const routines = await pool.query(`SELECT COUNT(*)::text AS count FROM pg_proc AS routine JOIN pg_namespace AS schema ON schema.oid = routine.pronamespace
    WHERE schema.nspname = current_schema() AND routine.proname IN ('gregale_mcp_task_check_admission', 'gregale_mcp_task_audit_admission')
      AND routine.pronargs = 0 AND routine.prorettype = 'trigger'::regtype AND routine.prosecdef
      AND routine.proconfig @> ARRAY[format('search_path=%I, pg_temp', current_schema())]`);
  if (Number(routines.rows[0]?.count) !== 2) throw new Error('MCP Task schema trigger security is incompatible; use the matching migration');
  const activated = await pool.query("SELECT namespace FROM gregale_mcp_task_admission_namespaces WHERE namespace = $1 AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid = 'gregale_mcp_tasks'::regclass AND tgname = 'gregale_mcp_tasks_admission_' || md5($1) AND tgenabled IN ('O', 'A') AND tgfoid = 'gregale_mcp_task_check_admission()'::regprocedure AND NOT tgisinternal)", [namespace]);
  if (!activated.rows.length) throw new Error('MCP Task namespace is not migrated or its admission trigger is disabled');
  return { version: MCP_TASK_SCHEMA_VERSION, role, namespacePrepared: true };
}

// Roles must already exist. The migration account owns the dedicated schema and
// objects; grant profiles give application accounts only the required DML.
export async function planMcpTaskDatabaseGrants({ pool, role, databaseRole }) {
  const privileges = Object.hasOwn(MCP_TASK_DATABASE_PROFILES, role) ? MCP_TASK_DATABASE_PROFILES[role] : undefined;
  if (!privileges || typeof databaseRole !== 'string' || !/^[A-Za-z_][A-Za-z0-9_]{0,62}$/.test(databaseRole)) throw new Error('Invalid MCP Task database role');
  await assertMcpTaskSchemaTrust(pool);
  const result = await pool.query('SELECT current_schema() AS schema');
  if (!result.rows[0]?.schema) throw new Error('MCP Task schema is unavailable');
  const schema = result.rows[0].schema;
  const quote = value => '"' + value.replaceAll('"', '""') + '"';
  const target = quote(databaseRole);
  const statements = [`GRANT USAGE ON SCHEMA ${quote(schema)} TO ${target}`];
  for (const [table, required] of Object.entries(privileges)) statements.push(`GRANT ${required.join(', ')} ON TABLE ${quote(schema)}.${quote(table)} TO ${target}`);
  if (role === 'runtime') statements.push(`GRANT USAGE ON SEQUENCE ${quote(schema)}.gregale_mcp_task_claim_order_seq TO ${target}`);
  for (const name of role === 'operator' ? ['gregale_mcp_task_audit_admission'] : role === 'runtime' ? ['gregale_mcp_task_check_admission', 'gregale_mcp_task_seed_fairness', 'gregale_mcp_task_notify_change'] : []) statements.push(`GRANT EXECUTE ON FUNCTION ${quote(schema)}.${name}() TO ${target}`);
  return { role, databaseRole, schema, statements };
}
