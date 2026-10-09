import pg from 'pg'
import { realpath } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { inspectClient } from './rpc-runtime/types.mjs'
import { limits } from './rpc-runtime/config.mjs'

export function options(args) {
  const result = { apply: false }
  for (let i = 0; i < args.length; i++) {
    if (args[i] === '--apply' && !result.apply) result.apply = true
    else if (args[i] === '--role' && result.role === undefined && args[i + 1]) result.role = args[++i]
    else throw new Error('Invalid permission options')
  }
  return result
}

const roleQuery = `SELECT r.oid, r.rolname AS name, r.rolcanlogin AS login,
 r.rolsuper OR r.rolcreatedb OR r.rolcreaterole OR r.rolreplication OR r.rolbypassrls OR r.rolinherit
 OR EXISTS(SELECT 1 FROM pg_auth_members m WHERE m.member=r.oid) AS elevated,
 COALESCE(shobj_description(r.oid,'pg_authid'),'') AS marker,
 COALESCE(r.rolconfig, ARRAY[]::text[]) AS settings
 FROM pg_roles r WHERE ($1::text IS NOT NULL AND r.rolname::text=$1::text)
 OR ($1::text IS NULL AND shobj_description(r.oid,'pg_authid')=$2 AND r.rolcanlogin)
 ORDER BY r.rolname`

async function rolePlan(client, requested) {
  const context = (await client.query(`SELECT current_user AS owner,
    shobj_description((SELECT oid FROM pg_roles WHERE rolname=session_user),'pg_authid') AS marker,
    n.nspowner=(SELECT oid FROM pg_roles WHERE rolname=current_user) AS owns
    FROM pg_namespace n WHERE n.nspname='api'`)).rows[0]
  if (!context?.owns) throw new Error('API schema owner required')
  const marker = /^gregale:credential:v1:([a-f0-9]{40}):migration$/.exec(context.marker ?? '')
  if (!requested && !marker) throw new Error('Automatic discovery requires a marked migration binding; use an explicit role')
  const expected = marker ? `gregale:credential:v1:${marker[1]}:data_api` : null
  const roles = (await client.query(roleQuery, [requested ?? null, expected])).rows
  if (roles.length !== 1) {
    const error = new Error('Exactly one active Data API role is required; select the binding role explicitly during rotation')
    error.report = { ready: false, blockers: ['role_selection_required'], candidates: roles.map(r => r.name) }
    throw error
  }
  const role = roles[0]
  if (!role.login || role.elevated || role.settings.length !== 1 || role.settings[0] !== `statement_timeout=${limits.queryMs}` ||
      (expected && role.marker !== expected) || (!expected && role.marker && !/^gregale:credential:v1:[a-f0-9]{40}:data_api$/.test(role.marker))) throw new Error('Unsafe or mismatched Data API role')
  return { role, owner: context.owner }
}

async function blockers(client, role) {
  const result = await client.query(`SELECT
    NOT has_database_privilege($1,current_database(),'CONNECT') OR has_database_privilege($1,current_database(),'CREATE,TEMPORARY') AS database_unsafe,
    NOT has_schema_privilege($1,'api','USAGE') OR EXISTS(SELECT 1 FROM pg_namespace n WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema' AND has_schema_privilege($1,n.oid,'CREATE')) AS schema_unsafe,
    EXISTS(SELECT 1 FROM pg_shdepend WHERE refclassid='pg_authid'::regclass AND refobjid=$2::oid AND deptype='o') AS owns_objects,
    EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname NOT IN ('api','information_schema') AND n.nspname !~ '^pg_' AND c.relkind IN ('r','p','v','m','f') AND has_table_privilege($1,c.oid,'SELECT,INSERT,UPDATE,DELETE')) AS outside_table_access,
    EXISTS(SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname IN ('api','public') AND p.prosecdef AND has_function_privilege($1,p.oid,'EXECUTE')) AS definer_execution`, [role.name, role.oid])
  const problems = Object.entries(result.rows[0]).filter(([, value]) => value).map(([key]) => key)
  const relations = await client.query(`SELECT c.relname AS name FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
    WHERE n.nspname='api' AND ((c.relkind IN ('r','p','v','m','f') AND (
      NOT has_table_privilege($1,c.oid,'SELECT') OR NOT has_table_privilege($1,c.oid,'INSERT') OR
      NOT has_table_privilege($1,c.oid,'UPDATE') OR NOT has_table_privilege($1,c.oid,'DELETE') OR
      (c.relkind IN ('r','p') AND (NOT c.relrowsecurity OR NOT EXISTS(SELECT 1 FROM pg_policy p WHERE p.polrelid=c.oid))) OR
      (c.relkind='v' AND NOT COALESCE(c.reloptions @> ARRAY['security_invoker=true'],false)) OR c.relkind IN ('m','f')))
      OR (c.relkind='S' AND (NOT has_sequence_privilege($1,c.oid,'USAGE') OR NOT has_sequence_privilege($1,c.oid,'SELECT'))))
    ORDER BY c.relname`, [role.name])
  const defaults = await client.query(`SELECT d.defaclobjtype AS kind, a.privilege_type AS privilege
    FROM pg_default_acl d JOIN pg_namespace n ON n.oid=d.defaclnamespace
    CROSS JOIN LATERAL aclexplode(d.defaclacl) a
    WHERE n.nspname='api' AND d.defaclrole=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND a.grantee=$1::oid`, [role.oid])
  const granted = new Set(defaults.rows.map(row => `${row.kind}:${row.privilege}`))
  if (!['SELECT','INSERT','UPDATE','DELETE'].every(p => granted.has(`r:${p}`))) problems.push('future_table_grants_missing')
  if (!['SELECT','USAGE'].every(p => granted.has(`S:${p}`))) problems.push('future_sequence_grants_missing')
  problems.push(...relations.rows.map(row => `relation_not_ready:${row.name}`))
  return problems
}

export async function permissions(connectionString, { role: requested, apply = false } = {}) {
  if (!connectionString || typeof apply !== 'boolean' || (requested !== undefined && (typeof requested !== 'string' || !requested || requested.includes('\0')))) throw new Error('Invalid permission setup')
  const client = new pg.Client({ connectionString, connectionTimeoutMillis: 5000, query_timeout: 30000 })
  try {
    await client.connect()
    await client.query(apply ? 'BEGIN ISOLATION LEVEL READ COMMITTED' : 'BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY')
    await client.query("SET LOCAL statement_timeout='30s'; SET LOCAL lock_timeout='1s'")
    if (apply) await client.query('SELECT pg_advisory_xact_lock(734928147)') // Same lock as the migration runner.
    const { role, owner } = await rolePlan(client, requested)
    const issues = await blockers(client, role)
    const snapshot = await inspectClient(client, ['api'], { includeUnexecutable: true })
    const supported = new Set(snapshot.functions.map(f => f.name))
    const functions = (await client.query(`SELECT p.oid, p.proname AS name, r.rolname AS owner,
      EXISTS(SELECT 1 FROM aclexplode(COALESCE(p.proacl,acldefault('f',p.proowner))) a WHERE a.grantee=$1::oid AND a.privilege_type='EXECUTE') AS granted,
      format('GRANT EXECUTE ON FUNCTION %I.%I(%s) TO %I',n.nspname,p.proname,pg_get_function_identity_arguments(p.oid),$2::text) AS sql
      FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles r ON r.oid=p.proowner
      WHERE n.nspname='api' AND obj_description(p.oid,'pg_proc')='@gregale:rpc' ORDER BY p.proname,p.oid LIMIT ${limits.relations + 1}`,  [role.oid, role.name])).rows
    if (functions.length > limits.relations) throw new Error('Too many opted-in functions')
    for (const fn of functions) if (fn.owner !== owner || !supported.has(fn.name)) issues.push(`unsupported_or_unowned_function:${fn.name}`)
    const missing = functions.filter(fn => !fn.granted)
    if (apply && issues.length) throw new Error('Permission setup blocked')
    if (apply) {
      for (const fn of missing) await client.query(fn.sql)
      if ((await blockers(client, role)).length) throw new Error('Permission readiness changed')
      const verified = await client.query('SELECT bool_and(has_function_privilege($1,p,\'EXECUTE\')) AS ready FROM unnest($2::oid[]) p', [role.name, functions.map(fn => fn.oid)])
      if (functions.length && !verified.rows[0].ready) throw new Error('Function grants incomplete')
    }
    await client.query('COMMIT')
    return { mode: apply ? 'apply' : 'dry-run', role: role.name, functions: functions.map(fn => ({ name: fn.name, supported: supported.has(fn.name) && fn.owner === owner, granted: apply || fn.granted })), missing: apply ? [] : missing.map(fn => fn.name), blockers: issues, ready: issues.length === 0 && (apply || missing.length === 0), refreshRequired: apply && missing.length > 0 }
  } catch (error) { try { await client.query('ROLLBACK') } catch {}; throw error }
  finally { await client.end() }
}

if (process.argv[1] && await realpath(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { const report = await permissions(process.env.MIGRATION_DATABASE_URL, options(process.argv.slice(2))); console.log(JSON.stringify(report)); if (!report.ready) process.exitCode = 1 }
  catch (error) { if (error.report) console.log(JSON.stringify(error.report)); console.error('RPC permission setup failed: check the owner migration binding, role selection, function eligibility and schema permissions.'); process.exitCode = 1 }
}
