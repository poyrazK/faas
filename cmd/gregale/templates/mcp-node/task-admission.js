const TABLE = 'gregale_mcp_task_admission';
const TASKS = 'gregale_mcp_tasks';

export function validateMcpTaskAdmissionHandlers(handlers) {
  if (!Array.isArray(handlers) || handlers.length > 4096 || handlers.some(handler => !handler || typeof handler.name !== 'string' || !/^[a-z][a-z0-9_.-]{0,127}$/.test(handler.name) || typeof handler.version !== 'string' || !/^[A-Za-z0-9._-]{1,64}$/.test(handler.version))) throw new Error('Invalid MCP Task admission handlers');
  return handlers;
}

export async function initializeMcpTaskAdmission(client, namespace, handlers = [], { schema = true } = {}) {
  validateMcpTaskAdmissionHandlers(handlers);
  if (schema) {
    await client.query(`CREATE TABLE IF NOT EXISTS gregale_mcp_task_admission_namespaces (
      namespace text PRIMARY KEY, activated_at timestamptz NOT NULL DEFAULT clock_timestamp()
    )`);
    await client.query(`CREATE TABLE IF NOT EXISTS ${TABLE} (
      namespace text NOT NULL, tool_name text NOT NULL, handler_version text NOT NULL,
      enabled boolean NOT NULL DEFAULT true, retired_at timestamptz,
      updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
      PRIMARY KEY (namespace, tool_name, handler_version),
      CHECK (NOT enabled OR retired_at IS NULL)
    )`);
    await client.query(`CREATE TABLE IF NOT EXISTS gregale_mcp_task_admission_audit (
      event_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
      namespace text NOT NULL, tool_name text NOT NULL, handler_version text NOT NULL,
      enabled boolean NOT NULL, retired_at timestamptz,
      changed_at timestamptz NOT NULL DEFAULT clock_timestamp(), database_role text NOT NULL
    )`);
    await client.query(`CREATE OR REPLACE FUNCTION gregale_mcp_task_audit_admission() RETURNS trigger
      LANGUAGE plpgsql AS $$ BEGIN
        IF TG_OP = 'INSERT' OR NEW.enabled IS DISTINCT FROM OLD.enabled OR NEW.retired_at IS DISTINCT FROM OLD.retired_at THEN
          INSERT INTO gregale_mcp_task_admission_audit (namespace, tool_name, handler_version, enabled, retired_at, database_role)
          VALUES (NEW.namespace, NEW.tool_name, NEW.handler_version, NEW.enabled, NEW.retired_at, CASE WHEN current_setting('role', true) = 'none' THEN session_user ELSE current_setting('role', true) END);
        END IF;
        RETURN NEW;
      END; $$`);
    await client.query(`DO $body$ BEGIN
      IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid = '${TABLE}'::regclass AND tgname = 'gregale_mcp_task_admission_audit' AND NOT tgisinternal) THEN
        CREATE TRIGGER gregale_mcp_task_admission_audit AFTER INSERT OR UPDATE ON ${TABLE}
        FOR EACH ROW EXECUTE FUNCTION gregale_mcp_task_audit_admission();
      END IF;
    END; $body$`);
  }
  // Preserve existing versions when introducing the gate, and never reopen
  // operator-disabled rows. Activated namespaces require a registered row.
  const activated = await client.query('SELECT namespace FROM gregale_mcp_task_admission_namespaces WHERE namespace = $1', [namespace]);
  if (!activated.rows.length) {
    await client.query('LOCK TABLE gregale_mcp_tasks IN SHARE ROW EXCLUSIVE MODE');
    await client.query(`INSERT INTO ${TABLE} (namespace, tool_name, handler_version)
      SELECT DISTINCT namespace, tool_name, handler_version FROM ${TASKS}
      WHERE namespace = $1 AND expires_at > clock_timestamp() ON CONFLICT DO NOTHING`, [namespace]);
    await client.query(`INSERT INTO ${TABLE} (namespace, tool_name, handler_version)
      SELECT DISTINCT namespace, handler.name, handler.version FROM gregale_mcp_task_workers
      CROSS JOIN LATERAL jsonb_to_recordset(handlers) AS handler(name text, version text)
      WHERE namespace = $1 AND expires_at > clock_timestamp() ON CONFLICT DO NOTHING`, [namespace]);
  }
  for (const handler of handlers) {
    await client.query(`INSERT INTO ${TABLE} (namespace, tool_name, handler_version)
      VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, [namespace, handler.name, handler.version]);
  }
  if (schema) {
    await client.query(`CREATE OR REPLACE FUNCTION gregale_mcp_task_check_admission() RETURNS trigger
      LANGUAGE plpgsql AS $$ DECLARE permitted boolean; BEGIN
        SELECT enabled INTO permitted FROM ${TABLE}
        WHERE namespace = NEW.namespace AND tool_name = NEW.tool_name AND handler_version = NEW.handler_version
        FOR SHARE;
        IF permitted IS DISTINCT FROM true THEN
          RAISE EXCEPTION 'MCP Task handler version is not accepting new Tasks'
            USING ERRCODE = '23514', CONSTRAINT = 'gregale_mcp_task_admission';
        END IF;
        RETURN NEW;
      END; $$`);
    await client.query(`DO $body$ DECLARE trusted_schema text := current_schema(); BEGIN
      IF EXISTS (SELECT 1 FROM pg_namespace CROSS JOIN LATERAL aclexplode(COALESCE(nspacl, acldefault('n', nspowner))) AS acl
        WHERE nspname = trusted_schema AND acl.grantee = 0 AND acl.privilege_type = 'CREATE') THEN
        RAISE EXCEPTION 'MCP Task schema must not grant CREATE to PUBLIC';
      END IF;
      EXECUTE format('ALTER FUNCTION %I.gregale_mcp_task_check_admission() SECURITY DEFINER SET search_path = %I, pg_temp', trusted_schema, trusted_schema);
      EXECUTE format('ALTER FUNCTION %I.gregale_mcp_task_audit_admission() SECURITY DEFINER SET search_path = %I, pg_temp', trusted_schema, trusted_schema);
      EXECUTE format('REVOKE ALL ON FUNCTION %I.gregale_mcp_task_check_admission() FROM PUBLIC', trusted_schema);
      EXECUTE format('REVOKE ALL ON FUNCTION %I.gregale_mcp_task_audit_admission() FROM PUBLIC', trusted_schema);
    END; $body$`);
  }
  // Use namespace-specific trigger WHEN clauses rather than a policy-table
  // lookup to decide enforcement. PostgreSQL refreshes trigger definitions even
  // for older transaction snapshots; a stale snapshot cannot skip activation.
  await client.query('INSERT INTO gregale_mcp_task_admission_namespaces (namespace) VALUES ($1) ON CONFLICT DO NOTHING', [namespace]);
  await client.query(`DO $body$ DECLARE configured record; trigger_name text; BEGIN
    FOR configured IN SELECT namespace FROM gregale_mcp_task_admission_namespaces LOOP
      trigger_name := 'gregale_mcp_tasks_admission_' || md5(configured.namespace);
      IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid = '${TASKS}'::regclass AND tgname = trigger_name AND NOT tgisinternal) THEN
        EXECUTE format('CREATE TRIGGER %I BEFORE INSERT ON ${TASKS} FOR EACH ROW WHEN (NEW.namespace = %L) EXECUTE FUNCTION gregale_mcp_task_check_admission()', trigger_name, configured.namespace);
      END IF;
    END LOOP;
  END; $body$`);
}

const STATUS = `SELECT admission.tool_name, admission.handler_version, admission.enabled, admission.retired_at, admission.updated_at,
  COUNT(task.task_id)::text AS retained_tasks, MAX(task.expires_at) AS latest_expiry
  FROM ${TABLE} AS admission LEFT JOIN ${TASKS} AS task
    ON task.namespace = admission.namespace AND task.tool_name = admission.tool_name AND task.handler_version = admission.handler_version
    AND task.expires_at > clock_timestamp() AND task.status IN ('queued', 'running', 'input_required')
  WHERE admission.namespace = $1
  GROUP BY admission.namespace, admission.tool_name, admission.handler_version
  ORDER BY admission.tool_name, admission.handler_version`;

export function createMcpTaskAdmissionController({ pool, namespace }) {
  if (!pool || typeof pool.query !== 'function' || typeof namespace !== 'string' || !namespace.trim()) throw new Error('Invalid MCP Task admission settings');
  return {
    async status() {
      const activated = await pool.query("SELECT namespace FROM gregale_mcp_task_admission_namespaces WHERE namespace = $1 AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid = 'gregale_mcp_tasks'::regclass AND tgname = 'gregale_mcp_tasks_admission_' || md5($1) AND tgenabled IN ('O', 'A') AND NOT tgisinternal)", [namespace]);
      const result = await pool.query(STATUS, [namespace]);
      return { enforced: activated.rows.length > 0, versions: result.rows.map(row => ({ tool: row.tool_name, version: row.handler_version,
        state: row.enabled ? 'allowed' : row.retired_at ? 'retired' : 'draining',
        canRetire: !row.enabled && !row.retired_at && Number(row.retained_tasks) === 0,
        retainedTasks: Number(row.retained_tasks), latestExpiry: row.latest_expiry ? new Date(row.latest_expiry).toISOString() : null,
        updatedAt: new Date(row.updated_at).toISOString(),
      })) };
    },
    async change(action, name, version) {
      if (!['allow', 'disable', 'retire'].includes(action)) throw new Error('Invalid MCP Task admission action');
      validateMcpTaskAdmissionHandlers([{ name, version }]);
      if (typeof pool.connect !== 'function') throw new Error('MCP Task admission changes require a transactional database connection');
      const client = await pool.connect();
      try {
        await client.query('BEGIN');
        // Serialize registry administration with schema initialization.
        await client.query('SELECT pg_advisory_xact_lock(hashtextextended($1, 0))', [TASKS]);
        const activated = await client.query("SELECT namespace FROM gregale_mcp_task_admission_namespaces WHERE namespace = $1 AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid = 'gregale_mcp_tasks'::regclass AND tgname = 'gregale_mcp_tasks_admission_' || md5($1) AND tgenabled IN ('O', 'A') AND NOT tgisinternal)", [namespace]);
        if (!activated.rows.length) throw new Error('Initialize admission and enable its trigger before changing handler policy');
        if (action === 'retire') {
          const selected = await client.query(`SELECT enabled FROM ${TABLE} WHERE namespace = $1 AND tool_name = $2 AND handler_version = $3 FOR UPDATE`, [namespace, name, version]);
          if (!selected.rows.length || selected.rows[0].enabled) throw new Error('Disable admission before retiring a handler version');
          const pending = await client.query(`SELECT COUNT(*)::text AS count FROM ${TASKS} WHERE namespace = $1 AND tool_name = $2 AND handler_version = $3 AND expires_at > clock_timestamp() AND status IN ('queued', 'running', 'input_required')`, [namespace, name, version]);
          if (Number(pending.rows[0].count) !== 0) throw new Error('Retained Tasks still require this handler version');
          await client.query(`UPDATE ${TABLE} SET retired_at = COALESCE(retired_at, clock_timestamp()), updated_at = clock_timestamp() WHERE namespace = $1 AND tool_name = $2 AND handler_version = $3`, [namespace, name, version]);
        } else {
          await client.query(`INSERT INTO ${TABLE} (namespace, tool_name, handler_version, enabled) VALUES ($1, $2, $3, $4)
            ON CONFLICT (namespace, tool_name, handler_version) DO UPDATE
            SET enabled = EXCLUDED.enabled, retired_at = CASE WHEN EXCLUDED.enabled THEN NULL ELSE gregale_mcp_task_admission.retired_at END, updated_at = clock_timestamp()`, [namespace, name, version, action === 'allow']);
        }
        await client.query('COMMIT');
      } catch (error) {
        await client.query('ROLLBACK').catch(() => {});
        throw error;
      } finally { client.release(); }
      return this.status();
    },
    async audit() {
      const result = await pool.query(`SELECT event_id::text, tool_name, handler_version, enabled, retired_at, changed_at, database_role
        FROM gregale_mcp_task_admission_audit WHERE namespace = $1 ORDER BY event_id DESC LIMIT 100`, [namespace]);
      return { events: result.rows };
    },
  };
}
