import pg from 'pg'
import { pathToFileURL } from 'node:url'
import { createHash } from 'node:crypto'
import { databaseURL, limits } from './config.mjs'

// These exact bodies are also the version-one compatibility contract. Startup
// checks them through the serving login; owner installation is always explicit.
const enforceBody = `
DECLARE packet jsonb; remaining bigint; statement_ms bigint; lock_ms bigint;
BEGIN
  packet := coalesce(nullif(current_setting('request.jwt.claims', true), ''), '{}')::jsonb -> 'gregale_sql_deadline';
  IF packet IS NULL OR jsonb_typeof(packet) <> 'object' THEN
    RAISE SQLSTATE 'PGRST' USING MESSAGE = '{"code":"data_api_sql_deadline_invalid","message":"Invalid SQL deadline contract","details":null,"hint":null}', DETAIL = '{"status":500,"headers":{}}';
  END IF;
  IF NOT packet ?& ARRAY['version','expires_at_ms','coordinated_refresh']
     OR packet - ARRAY['version','expires_at_ms','coordinated_refresh'] <> '{}'::jsonb
     OR packet->'version' <> '1'::jsonb
     OR jsonb_typeof(packet->'expires_at_ms') <> 'number'
     OR length(packet->>'expires_at_ms') > 16
     OR packet->>'expires_at_ms' !~ '^[0-9]+$'
     OR jsonb_typeof(packet->'coordinated_refresh') <> 'boolean' THEN
    RAISE SQLSTATE 'PGRST' USING MESSAGE = '{"code":"data_api_sql_deadline_invalid","message":"Invalid SQL deadline contract","details":null,"hint":null}', DETAIL = '{"status":500,"headers":{}}';
  END IF;
  remaining := (packet->>'expires_at_ms')::bigint - floor(extract(epoch FROM clock_timestamp()) * 1000)::bigint;
  IF remaining > ${limits.queryMs} THEN
    RAISE SQLSTATE 'PGRST' USING MESSAGE = '{"code":"data_api_sql_deadline_invalid","message":"Invalid SQL deadline contract","details":null,"hint":null}', DETAIL = '{"status":500,"headers":{}}';
  END IF;
  IF remaining <= 0 THEN
    RAISE SQLSTATE 'PGRST' USING MESSAGE = '{"code":"data_api_sql_deadline_exceeded","message":"SQL request deadline exceeded","details":null,"hint":null}', DETAIL = '{"status":504,"headers":{}}';
  END IF;
  statement_ms := floor(extract(epoch FROM current_setting('statement_timeout')::interval) * 1000)::bigint;
  lock_ms := floor(extract(epoch FROM current_setting('lock_timeout')::interval) * 1000)::bigint;
  PERFORM set_config('statement_timeout', least(remaining, ${limits.queryMs}, coalesce(nullif(statement_ms, 0), ${limits.queryMs}))::text, true);
  PERFORM set_config('lock_timeout', least(remaining, ${limits.lockMs}, coalesce(nullif(lock_ms, 0), ${limits.lockMs}))::text, true);
END`

const requestBody = `
BEGIN
  PERFORM gregale_data_api.enforce_deadline();
  IF current_setting('request.jwt.claims', true)::jsonb -> 'gregale_sql_deadline' -> 'coordinated_refresh' = 'true'::jsonb THEN
    PERFORM gregale_data_api.check_request();
    PERFORM gregale_data_api.enforce_deadline();
  END IF;
END`

function identifier(role) {
  if (typeof role !== 'string' || !role || role.includes('\0')) throw new Error('Invalid SQL deadline binding role')
  return '"' + role.replaceAll('"', '""') + '"'
}

async function transactionLimits(client) {
  await client.query("SELECT set_config('statement_timeout',$1,true),set_config('lock_timeout',$2,true)", [String(limits.queryMs), String(limits.lockMs)])
}

async function restrictedRole(client, role) {
  const result = await client.query(`SELECT rolcanlogin AND NOT (rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls OR rolinherit)
    AND NOT EXISTS(SELECT 1 FROM pg_auth_members WHERE member=r.oid) AS safe FROM pg_roles r WHERE rolname=$1`, [role])
  if (result.rows[0]?.safe !== true) throw new Error('SQL deadline requires an unprivileged binding login without memberships')
}

async function checkClient(client, role, coordinatedRefresh) {
  await restrictedRole(client, role)
  const hooks = await client.query(`SELECT p.proname,
    p.prosrc=CASE p.proname WHEN 'enforce_deadline' THEN $1 ELSE $2 END
    AND l.lanname='plpgsql' AND p.prokind='f' AND p.pronargs=0 AND p.prorettype='void'::regtype
    AND NOT p.prosecdef AND NOT p.proretset AND NOT p.proisstrict AND NOT p.proleakproof
    AND p.provolatile='v' AND p.proparallel='u' AND p.proconfig=ARRAY['search_path=pg_catalog']
    AND NOT pg_has_role($3::name,p.proowner,'MEMBER') AND NOT pg_has_role($3::name,n.nspowner,'MEMBER')
    AND NOT has_schema_privilege($3::name,n.oid,'CREATE') AND has_schema_privilege($3::name,n.oid,'USAGE')
    AND has_function_privilege($3::name,p.oid,'EXECUTE')
    AND NOT EXISTS(SELECT 1 FROM aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a WHERE a.grantee=0 AND a.privilege_type='EXECUTE') AS valid
    FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang
    WHERE p.oid IN (to_regprocedure('gregale_data_api.enforce_deadline()'),to_regprocedure('gregale_data_api.check_deadline_request()'))`, [enforceBody, requestBody, role])
  if (hooks.rowCount !== 2 || hooks.rows.some(row => row.valid !== true)) throw new Error('Install the matching protected SQL deadline hooks and serving grants')
  if (coordinatedRefresh) {
    const refresh = await client.query(`SELECT count(*)=2 AND bool_and(p.prosecdef
      AND coalesce(p.proconfig @> ARRAY['search_path=pg_catalog'],false)
      AND NOT pg_has_role($1::name,p.proowner,'MEMBER') AND has_function_privilege($1::name,p.oid,'EXECUTE')) AS valid
      FROM pg_proc p WHERE p.oid IN (to_regprocedure('gregale_data_api.check_request()'),to_regprocedure('gregale_data_api.refresh_status(text)'))`, [role])
    if (refresh.rows[0]?.valid !== true) throw new Error('Install the protected coordinated refresh hooks before composing SQL deadlines')
  }
  return { version: 1, ready: true, coordinated_refresh: coordinatedRefresh }
}

export { checkClient as checkSQLDeadlineClient }

export async function installSQLDeadline(client, role, { coordinatedRefresh = false } = {}) {
  const name = identifier(role)
  if (typeof coordinatedRefresh !== 'boolean') throw new Error('Invalid SQL deadline refresh mode')
  await client.query('BEGIN')
  try {
    await transactionLimits(client)
    await restrictedRole(client, role)
    // External customer DDL follows the owner-installed refresh-hook boundary.
    // Identifiers are quoted; all other interpolated text is fixed source/limits.
    await client.query(`CREATE SCHEMA IF NOT EXISTS gregale_data_api;
      REVOKE ALL ON SCHEMA gregale_data_api FROM PUBLIC;
      CREATE OR REPLACE FUNCTION gregale_data_api.enforce_deadline() RETURNS void
        LANGUAGE plpgsql SECURITY INVOKER VOLATILE SET search_path=pg_catalog AS $deadline$${enforceBody}$deadline$;
      CREATE OR REPLACE FUNCTION gregale_data_api.check_deadline_request() RETURNS void
        LANGUAGE plpgsql SECURITY INVOKER VOLATILE SET search_path=pg_catalog AS $deadline$${requestBody}$deadline$;
      REVOKE ALL ON FUNCTION gregale_data_api.enforce_deadline() FROM PUBLIC;
      REVOKE ALL ON FUNCTION gregale_data_api.check_deadline_request() FROM PUBLIC;
      GRANT USAGE ON SCHEMA gregale_data_api TO ${name};
      GRANT EXECUTE ON FUNCTION gregale_data_api.enforce_deadline() TO ${name};
      GRANT EXECUTE ON FUNCTION gregale_data_api.check_deadline_request() TO ${name};`)
    const report = await checkClient(client, role, coordinatedRefresh)
    await client.query('COMMIT')
    return report
  } catch (error) { await client.query('ROLLBACK'); throw error }
}

export async function checkSQLDeadlineSetup(client, role, { coordinatedRefresh = false } = {}) {
  identifier(role)
  if (typeof coordinatedRefresh !== 'boolean') throw new Error('Invalid SQL deadline refresh mode')
  await client.query('BEGIN READ ONLY')
  try {
    await transactionLimits(client)
    return await checkClient(client, role, coordinatedRefresh)
  } finally { await client.query('ROLLBACK') }
}

const policyTable = 'gregale_data_api.deadline_binding_policies'
const bodyHash = body => createHash('sha256').update(body).digest('hex')

async function enrollmentContext(client, role, bindingID, coordinatedRefresh, removing) {
  identifier(role)
  if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(bindingID) || /^0{8}-0{4}-0{4}-0{4}-0{12}$/.test(bindingID)) throw new Error('Invalid SQL deadline binding ID')
  if (typeof coordinatedRefresh !== 'boolean' || (!removing && coordinatedRefresh)) throw new Error('Automatic deadline grants require deadline-only refresh mode')
  const result = await client.query(`SELECT n.nspowner::regrole::text AS owner,
    shobj_description(r.oid,'pg_authid') AS marker,
    current_user=n.nspowner::regrole::text
    AND n.nspowner=(SELECT nspowner FROM pg_namespace WHERE nspname='api')
    AND n.nspowner=(SELECT nspowner FROM pg_namespace WHERE nspname='public')
    AND shobj_description(n.nspowner,'pg_authid')='gregale:schema-owner:v1'
    AND ($2 OR to_regclass('gregale_data_api.refresh_gate') IS NULL) AS valid
    FROM pg_namespace n CROSS JOIN pg_roles r WHERE n.nspname='gregale_data_api' AND r.rolname=$1`, [role, removing])
  const row = result.rows[0], scope = /^gregale:credential:v1:([0-9a-f]{40}):data_api$/.exec(row?.marker ?? '')?.[1]
  if (!scope || row?.valid !== true || !/^gregale_schema_[0-9a-f]{32}$/.test(row.owner)) throw new Error('Enrollment requires the managed schema owner, binding marker and a database without coordinated refresh gates')
  await client.query('SELECT pg_advisory_xact_lock(hashtextextended($1,0))', [row.owner])
  return scope
}

async function checkPolicyTable(client) {
  const result = await client.query(`SELECT c.relkind='r' AND c.relpersistence='p' AND NOT c.relrowsecurity AND NOT c.relforcerowsecurity
    AND c.relowner=(current_user::regrole)::oid AND n.nspowner=c.relowner
    AND NOT EXISTS(SELECT 1 FROM aclexplode(coalesce(c.relacl,acldefault('r',c.relowner))) a WHERE a.grantee<>c.relowner)
    AND NOT EXISTS(SELECT 1 FROM pg_attribute a, LATERAL aclexplode(a.attacl) x WHERE a.attrelid=c.oid AND x.grantee<>c.relowner)
    AND NOT EXISTS(SELECT 1 FROM aclexplode(coalesce(n.nspacl,acldefault('n',n.nspowner))) a WHERE a.grantee=0)
    AND NOT EXISTS(SELECT 1 FROM pg_inherits WHERE inhrelid=c.oid OR inhparent=c.oid)
    AND (SELECT array_agg(a.attname::text||':'||a.atttypid::regtype::text||':'||a.attnotnull::text ORDER BY a.attnum)
      FROM pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped)
      =ARRAY['binding_id:uuid:true','scope:text:true','version:integer:true','enforce_sha256:text:true','request_sha256:text:true']
    AND EXISTS(SELECT 1 FROM pg_constraint p WHERE p.conrelid=c.oid AND p.contype='p' AND p.convalidated AND p.conkey=ARRAY[1,2]::smallint[]) AS valid
    FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid=to_regclass('${policyTable}')`)
  if (result.rows[0]?.valid !== true) throw new Error('SQL deadline enrollment table ownership, shape or permissions have drifted')
}

// Enrollment never installs or repairs hooks. Only the stable managed schema
// owner can approve a binding; serving logins cannot read or edit this table.
export async function manageSQLDeadlineEnrollment(client, action, role, bindingID, { coordinatedRefresh = false } = {}) {
  if (!['enroll','check-enrollment','unenroll'].includes(action)) throw new Error('Invalid SQL deadline enrollment action')
  await client.query(action === 'check-enrollment' ? 'BEGIN READ ONLY' : 'BEGIN')
  try {
    await transactionLimits(client)
    const scope = await enrollmentContext(client, role, bindingID, coordinatedRefresh, action === 'unenroll')
    if (action !== 'unenroll') {
      await checkClient(client, role, false)
      const owned = await client.query(`SELECT count(*)=2 AND bool_and(p.proowner=(current_user::regrole)::oid) AS valid FROM pg_proc p
        WHERE p.oid IN (to_regprocedure('gregale_data_api.enforce_deadline()'),to_regprocedure('gregale_data_api.check_deadline_request()'))`)
      if (owned.rows[0]?.valid !== true) throw new Error('SQL deadline hooks must belong to the managed schema owner')
    }
    if (action === 'enroll') {
      await client.query(`CREATE TABLE IF NOT EXISTS ${policyTable} (
        binding_id uuid NOT NULL, scope text NOT NULL, version integer NOT NULL,
        enforce_sha256 text NOT NULL, request_sha256 text NOT NULL, PRIMARY KEY(binding_id,scope))`)
    }
    await checkPolicyTable(client)
    const values = [bindingID, scope, bodyHash(enforceBody), bodyHash(requestBody)]
    if (action === 'enroll') {
      // Repeated enrollment is safe; conflicting approvals require an explicit
      // unenroll rather than silently replacing an unknown version or hash.
      await client.query(`INSERT INTO ${policyTable} VALUES($1::uuid,$2,1,$3,$4) ON CONFLICT DO NOTHING`, values)
    } else if (action === 'unenroll') {
      await client.query(`DELETE FROM ONLY ${policyTable} WHERE binding_id=$1::uuid AND scope=$2`, values.slice(0,2))
    }
    if (action !== 'unenroll') {
      const row = await client.query(`SELECT version=1 AND enforce_sha256=$3 AND request_sha256=$4 AS valid
        FROM ONLY ${policyTable} WHERE binding_id=$1::uuid AND scope=$2`, values)
      if (row.rowCount !== 1 || row.rows[0].valid !== true) throw new Error('SQL deadline binding enrollment is absent or incompatible')
      await checkClient(client, role, false)
    }
    await client.query(action === 'check-enrollment' ? 'ROLLBACK' : 'COMMIT')
    return { version: 1, enrolled: action !== 'unenroll', coordinated_refresh: false }
  } catch (error) { await client.query('ROLLBACK'); throw error }
}

// A single startup connection closes before serving/engine pools are opened.
export async function checkSQLDeadlineConnection(connection, options = {}) {
  const client = new pg.Client({ connectionString: databaseURL(connection), connectionTimeoutMillis: limits.queryMs, query_timeout: limits.queryMs })
  try {
    await client.connect()
    const role = (await client.query('SELECT current_user AS name')).rows[0].name
    return await checkSQLDeadlineSetup(client, role, options)
  } finally { await client.end() }
}

export async function main(env = process.env, args = process.argv.slice(2)) {
  const [action, role, bindingID] = args
  const enrollment = ['enroll','check-enrollment','unenroll'].includes(action)
  if (args.length !== (enrollment ? 3 : 2) || !['install','check','enroll','check-enrollment','unenroll'].includes(action) || !env.MIGRATION_DATABASE_URL) throw new Error('Use sql-deadline.mjs install|check BINDING_ROLE or enroll|check-enrollment|unenroll BINDING_ROLE BINDING_ID with MIGRATION_DATABASE_URL')
  if (env.DATA_API_COORDINATED_REFRESH !== undefined && env.DATA_API_COORDINATED_REFRESH !== '1') throw new Error('Invalid coordinated refresh setting')
  const client = new pg.Client({ connectionString: databaseURL(env.MIGRATION_DATABASE_URL), connectionTimeoutMillis: limits.queryMs, query_timeout: limits.queryMs })
  try {
    await client.connect()
    const options = { coordinatedRefresh: env.DATA_API_COORDINATED_REFRESH === '1' }
    const report = enrollment
      ? await manageSQLDeadlineEnrollment(client, action, role, bindingID, options)
      : await (action === 'install' ? installSQLDeadline : checkSQLDeadlineSetup)(client, role, options)
    process.stdout.write(JSON.stringify({ action, ...report }) + '\n')
  } finally { await client.end() }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) main().catch(() => {
  process.stderr.write('SQL deadline setup failed; check owner authority, protected hook versions, serving grants and refresh compatibility.\n')
  process.exitCode = 1
})
