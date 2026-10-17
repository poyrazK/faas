import pg from 'pg'
import { createHash } from 'node:crypto'
import { pathToFileURL } from 'node:url'
import { inspectClient, fingerprint as schemaFingerprint } from './types.mjs'
import { schemaNames, databaseURL } from './config.mjs'

const hash = /^[a-f0-9]{64}$/
export function refreshTokenHash(token) {
  return typeof token === 'string' && hash.test(token) ? createHash('sha256').update(token).digest('hex') : null
}

function roleIdentifier(role) {
  if (typeof role !== 'string' || !role || role.includes('\0')) throw new Error('Invalid binding role')
  return '"' + role.replaceAll('"', '""') + '"'
}

// Owner-only installation. The binding can execute the two read/check functions,
// but cannot read or mutate the control table. This schema is never exposed.
export async function installRefreshBarrier(client, role, schemas = ['api']) {
  const identifier = roleIdentifier(role)
  await client.query('BEGIN')
  try {
    await client.query(`
      CREATE SCHEMA IF NOT EXISTS gregale_data_api;
      REVOKE ALL ON SCHEMA gregale_data_api FROM PUBLIC;
      REVOKE ALL ON SCHEMA gregale_data_api FROM ${identifier};
      CREATE TABLE IF NOT EXISTS gregale_data_api.refresh_gate (
        role_name text PRIMARY KEY, paused boolean NOT NULL DEFAULT false,
        token_hash text, fingerprint text NOT NULL,
        CHECK ((paused AND token_hash IS NOT NULL AND token_hash ~ '^[a-f0-9]{64}$') OR (NOT paused AND token_hash IS NULL)),
        CHECK (fingerprint ~ '^[a-f0-9]{64}$')
      );
      REVOKE ALL ON gregale_data_api.refresh_gate FROM PUBLIC;
      REVOKE ALL ON gregale_data_api.refresh_gate FROM ${identifier};
      CREATE OR REPLACE FUNCTION gregale_data_api.check_request() RETURNS void
      LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $function$
      DECLARE binding text := current_setting('role', true); gate record; claims jsonb;
      BEGIN
        -- Stronger isolation can retain a snapshot from before a waited-for pause.
        IF current_setting('transaction_isolation') <> 'read committed' THEN
          RAISE SQLSTATE 'PT503' USING MESSAGE = 'data_api_refresh_isolation_unsupported';
        END IF;
        IF binding IS NULL OR binding = 'none' THEN binding := session_user; END IF;
        -- Advisory transaction locks also work in PostgREST's read-only GET transactions.
        PERFORM pg_advisory_xact_lock_shared(hashtextextended('gregale-data-api:' || binding, 0));
        SELECT * INTO gate FROM gregale_data_api.refresh_gate WHERE role_name = binding;
        IF NOT FOUND THEN RAISE SQLSTATE 'PT503' USING MESSAGE = 'data_api_refresh_unavailable'; END IF;
        claims := coalesce(nullif(current_setting('request.jwt.claims', true), ''), '{}')::jsonb;
        IF gate.paused THEN
          IF gate.token_hash IS NULL OR claims->>'gregale_refresh_hash' IS DISTINCT FROM gate.token_hash THEN
            RAISE SQLSTATE 'PT503' USING MESSAGE = 'data_api_refresh_paused';
          END IF;
        ELSIF gate.fingerprint IS NULL THEN
          RAISE SQLSTATE 'PT503' USING MESSAGE = 'data_api_refresh_unavailable';
        ELSIF claims->>'gregale_serving_fingerprint' IS DISTINCT FROM gate.fingerprint THEN
          RAISE SQLSTATE 'PT409' USING MESSAGE = 'schema_contract_mismatch';
        END IF;
      END $function$;
      CREATE OR REPLACE FUNCTION gregale_data_api.refresh_status(candidate_hash text)
      RETURNS TABLE(paused boolean, fingerprint text, owns_pause boolean)
      LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog AS $function$
        SELECT g.paused, g.fingerprint, g.paused AND g.token_hash = candidate_hash
        FROM gregale_data_api.refresh_gate g WHERE role_name = session_user
      $function$;
      REVOKE ALL ON FUNCTION gregale_data_api.check_request() FROM PUBLIC;
      REVOKE ALL ON FUNCTION gregale_data_api.refresh_status(text) FROM PUBLIC;
      GRANT USAGE ON SCHEMA gregale_data_api TO ${identifier};
      GRANT EXECUTE ON FUNCTION gregale_data_api.check_request() TO ${identifier};
      GRANT EXECUTE ON FUNCTION gregale_data_api.refresh_status(text) TO ${identifier};
    `)
    const ownership = await client.query(`SELECT (pg_has_role(r.oid,n.nspowner,'MEMBER') OR pg_has_role(r.oid,c.relowner,'MEMBER') OR r.rolsuper OR r.rolbypassrls OR r.rolcreaterole OR r.rolcreatedb OR has_schema_privilege(r.oid,n.oid,'CREATE') OR has_table_privilege(r.oid,c.oid,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')) AS unsafe FROM pg_namespace n JOIN pg_class c ON c.relnamespace=n.oid JOIN pg_roles r ON r.rolname=$1 WHERE n.nspname='gregale_data_api' AND c.relname='refresh_gate'`, [role])
    if (ownership.rows[0]?.unsafe !== false) throw new Error('Serving role must remain restricted and cannot own or modify control state')
    await client.query('SET LOCAL ROLE ' + identifier)
    const initial = schemaFingerprint(await inspectClient(client, schemas))
    await client.query('RESET ROLE')
    await client.query('INSERT INTO gregale_data_api.refresh_gate(role_name,fingerprint) VALUES ($1,$2) ON CONFLICT DO NOTHING', [role,initial])
    await client.query('COMMIT')
  } catch (error) { await client.query('ROLLBACK'); throw error }
}

export async function changeRefreshBarrier(client, action, role, token, fingerprint, schemas = ['api']) {
  const identifier = roleIdentifier(role)
  const tokenHash = refreshTokenHash(token)
  if (!tokenHash || !['pause', 'resume'].includes(action) || (action === 'resume' && !hash.test(fingerprint ?? ''))) throw new Error('Invalid refresh operation')
  await client.query('BEGIN')
  try {
    // An exclusive transaction lock drains every transaction admitted by the hook.
    // New requests block behind it and observe the paused row before running SQL.
    await client.query("SELECT pg_advisory_xact_lock(hashtextextended('gregale-data-api:' || $1, 0))", [role])
    if (action === 'resume') {
      const gate = await client.query('SELECT 1 FROM gregale_data_api.refresh_gate WHERE role_name=$1 AND paused AND token_hash=$2', [role, tokenHash])
      if (gate.rowCount !== 1) throw new Error('Refresh owner changed')
      await client.query('SET LOCAL ROLE ' + identifier)
      const current = schemaFingerprint(await inspectClient(client, schemas))
      await client.query('RESET ROLE')
      if (current !== fingerprint) throw new Error('Schema changed after verification; keep the barrier paused')
    }
    const result = action === 'pause'
      ? await client.query('UPDATE gregale_data_api.refresh_gate SET paused=true, token_hash=$2 WHERE role_name=$1 AND NOT paused RETURNING role_name', [role, tokenHash])
      : await client.query('UPDATE gregale_data_api.refresh_gate SET paused=false, token_hash=NULL, fingerprint=$3 WHERE role_name=$1 AND paused AND token_hash=$2 RETURNING role_name', [role, tokenHash, fingerprint])
    if (result.rowCount !== 1) throw new Error('Refresh state or owner changed; leave the barrier paused and inspect recovery')
    await client.query('COMMIT')
  } catch (error) { await client.query('ROLLBACK'); throw error }
}

export function refreshStatusReader(connection) {
  const pool = new pg.Pool({ connectionString: connection, max: 1, connectionTimeoutMillis: 3000, query_timeout: 3000 })
  // An idle client dropped by a database restart must not crash the server;
  // driver errors can carry connection details, so report a fixed message.
  pool.on('error', () => console.error('refresh status database connection unavailable'))
  return {
    async read(token) {
      const result = await pool.query('SELECT * FROM gregale_data_api.refresh_status($1)', [refreshTokenHash(token)])
      if (result.rowCount !== 1) throw new Error('Refresh barrier missing')
      const row = result.rows[0]
      if (typeof row.paused !== 'boolean' || !hash.test(row.fingerprint ?? '')) throw new Error('Invalid refresh barrier state')
      return { ...row, owns_pause: row.owns_pause === true }
    },
    close: () => pool.end(),
  }
}

const setupFailures = {
  control_access: 'Owner connection needs installed control-schema usage and control-table SELECT/UPDATE privileges',
  serving_role: 'Serving role must remain restricted and cannot manage refresh state',
  barrier_state: 'Install a valid refresh barrier for this binding role before enabling coordination',
  assume_binding_role: 'Owner connection must be permitted to SET ROLE to the binding login',
  hook_access: 'Serving role needs refresh hook and status-function execution privileges, protected ownership, SECURITY DEFINER and a pg_catalog search path',
  schema_inspection: 'Check exposed schemas and role-filtered inspection permissions',
}
class RefreshSetupError extends Error {
  constructor(stage) { super(`Refresh setup ${stage}: ${setupFailures[stage]}`) }
}

// Inspect with a read-only transaction. Never install, pause, resume or adopt a gate.
export async function checkRefreshSetup(client, role, schemas = ['api']) {
  const identifier = roleIdentifier(role)
  await client.query('BEGIN READ ONLY')
  let stage = 'control_access'
  try {
    const authority = await client.query(`SELECT has_schema_privilege(current_user,'gregale_data_api','USAGE') AND has_table_privilege(current_user,'gregale_data_api.refresh_gate','SELECT') AND has_table_privilege(current_user,'gregale_data_api.refresh_gate','UPDATE') AS allowed`)
    if (authority.rows[0]?.allowed !== true) throw new Error('Owner connection needs control-schema usage and control-table SELECT/UPDATE privileges')
    stage = 'serving_role'
    const safety = await client.query(`SELECT r.rolsuper OR r.rolbypassrls OR r.rolcreaterole OR r.rolcreatedb OR pg_has_role(r.oid,c.relowner,'MEMBER') OR has_schema_privilege(r.oid,n.oid,'CREATE') OR has_table_privilege(r.oid,c.oid,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER') AS unsafe FROM pg_roles r JOIN pg_namespace n ON n.nspname='gregale_data_api' JOIN pg_class c ON c.relnamespace=n.oid AND c.relname='refresh_gate' WHERE r.rolname=$1`, [role])
    if (safety.rows[0]?.unsafe !== false) throw new Error('Serving role must remain restricted and cannot manage refresh state')
    stage = 'barrier_state'
    const gate = await client.query('SELECT paused, fingerprint FROM gregale_data_api.refresh_gate WHERE role_name=$1', [role])
    if (gate.rowCount !== 1 || typeof gate.rows[0].paused !== 'boolean' || !hash.test(gate.rows[0].fingerprint ?? '')) throw new Error('Install the refresh barrier for this binding role before enabling coordination')
    stage = 'assume_binding_role'
    await client.query('SET LOCAL ROLE ' + identifier)
    stage = 'hook_access'
    const hooks = await client.query(`SELECT has_schema_privilege(current_user,'gregale_data_api','USAGE') AND has_function_privilege(current_user,'gregale_data_api.check_request()','EXECUTE') AND has_function_privilege(current_user,'gregale_data_api.refresh_status(text)','EXECUTE') AS allowed`)
    if (hooks.rows[0]?.allowed !== true) throw new Error('Serving role needs refresh hook and status-function execution privileges')
    const protectedHooks = await client.query(`SELECT count(*)=2 AND bool_and(p.prosecdef AND coalesce(p.proconfig @> ARRAY['search_path=pg_catalog'],false) AND NOT pg_has_role(current_user,p.proowner,'MEMBER')) AS valid FROM pg_proc p WHERE p.oid IN (to_regprocedure('gregale_data_api.check_request()'),to_regprocedure('gregale_data_api.refresh_status(text)'))`)
    if (protectedHooks.rows[0]?.valid !== true) throw new Error('Refresh hooks must have protected ownership and SECURITY DEFINER with a fixed search path')
    stage = 'schema_inspection'
    const live = schemaFingerprint(await inspectClient(client, schemas))
    return { version: 1, ready: true, paused: gate.rows[0].paused, fingerprint: gate.rows[0].fingerprint, live_fingerprint: live }
  } catch { throw new RefreshSetupError(stage) } finally { await client.query('ROLLBACK') }
}

export async function main(env = process.env, args = process.argv.slice(2)) {
  const [action, role] = args
  if (args.length !== 2 || !['install', 'pause', 'resume', 'check'].includes(action)) throw new Error('Use refresh.mjs install|pause|resume|check BINDING_ROLE')
  const client = new pg.Client({ connectionString: databaseURL(env.MIGRATION_DATABASE_URL), connectionTimeoutMillis: 3000, query_timeout: 30000 })
  if (!env.MIGRATION_DATABASE_URL) throw new Error('MIGRATION_DATABASE_URL is required')
  await client.connect()
  try {
    if (action === 'check') {
      const result = await checkRefreshSetup(client, role, schemaNames(env.DATA_API_SCHEMAS))
      process.stdout.write(JSON.stringify(result) + '\n')
      return
    }
    if (action === 'install') await installRefreshBarrier(client, role, schemaNames(env.DATA_API_SCHEMAS))
    else await changeRefreshBarrier(client, action, role, env.GREGALE_DATA_API_REFRESH_TOKEN, env.GREGALE_DATA_API_REFRESH_FINGERPRINT, schemaNames(env.DATA_API_SCHEMAS))
    process.stdout.write(JSON.stringify({ action, status: 'completed' }) + '\n')
  } finally { await client.end() }
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) main().catch(error => { process.stderr.write((error instanceof RefreshSetupError ? error.message : 'Data API refresh control failed; verify barrier installation, owner SET ROLE and control-table privileges, and serving hook grants. Use data-api status before recovery.') + '\n'); process.exitCode = 1 })
