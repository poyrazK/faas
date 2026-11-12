import pg from 'pg'
import { createHash } from 'node:crypto'

const hash = value => createHash('sha256').update(JSON.stringify(value)).digest('hex')
const bound = 10000
const queries = [
  ['extension', `SELECT e.extname AS name,jsonb_build_object('version',e.extversion,'schema',n.nspname) AS definition FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE cardinality($1::text[])>0 ORDER BY 1 LIMIT 10001`],
  ['settings', `SELECT 'database' AS name,coalesce(to_jsonb((SELECT setconfig FROM pg_db_role_setting WHERE setrole=0 AND setdatabase=(SELECT oid FROM pg_database WHERE datname=current_database()))),'null'::jsonb) AS definition WHERE cardinality($1::text[])>0`],
  ['schema', `SELECT nspname AS name,jsonb_build_object('owner',nspowner::regrole::text,'acl',nspacl::text) AS definition FROM pg_namespace WHERE nspname=ANY($1) ORDER BY 1`],
  ['column', `SELECT n.nspname||'.'||c.relname||'.'||a.attname AS name,jsonb_build_object('acl',a.attacl::text,'required',a.attnotnull,'generated',a.attgenerated,'identity',a.attidentity) AS definition
    FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=ANY($1) AND c.relkind IN ('r','p','v','m') AND a.attnum>0 AND NOT a.attisdropped ORDER BY 1 LIMIT 10001`],
  ['relation', `SELECT n.nspname||'.'||c.relname AS name, jsonb_build_object('rls',c.relrowsecurity,'force_rls',c.relforcerowsecurity,'options',c.reloptions,
    'view',CASE WHEN c.relkind='v' THEN pg_get_viewdef(c.oid,true) END,'acl',c.relacl::text) AS definition
    FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=ANY($1) AND c.relkind IN ('r','p','v','m') ORDER BY 1 LIMIT 10001`],
  ['policy', `SELECT n.nspname||'.'||c.relname||'.'||p.polname AS name, jsonb_build_object('command',p.polcmd,'permissive',p.polpermissive,
    'roles',ARRAY(SELECT CASE WHEN r=0 THEN 'PUBLIC' ELSE r::regrole::text END FROM unnest(p.polroles) r ORDER BY 1),
    'using',pg_get_expr(p.polqual,p.polrelid),'check',pg_get_expr(p.polwithcheck,p.polrelid)) AS definition
    FROM pg_policy p JOIN pg_class c ON c.oid=p.polrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=ANY($1) ORDER BY 1 LIMIT 10001`],
  ['default', `SELECT n.nspname||'.'||c.relname||'.'||a.attname AS name, to_jsonb(pg_get_expr(d.adbin,d.adrelid)) AS definition
    FROM pg_attrdef d JOIN pg_class c ON c.oid=d.adrelid JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum=d.adnum
    WHERE n.nspname=ANY($1) ORDER BY 1 LIMIT 10001`],
  ['constraint', `SELECT n.nspname||'.'||c.relname||'.'||x.conname AS name, to_jsonb(pg_get_constraintdef(x.oid,true)) AS definition
    FROM pg_constraint x JOIN pg_class c ON c.oid=x.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=ANY($1) ORDER BY 1 LIMIT 10001`],
  ['trigger', `SELECT n.nspname||'.'||c.relname||'.'||t.tgname AS name, jsonb_build_object('enabled',t.tgenabled,'definition',pg_get_triggerdef(t.oid,true)) AS definition
    FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=ANY($1) AND NOT t.tgisinternal ORDER BY 1 LIMIT 10001`],
  // Include private helper schemas: an RLS policy can call a helper outside
  // the exposed schema, including through another function.
  ['function', `SELECT n.nspname||'.'||p.proname||'('||pg_get_function_identity_arguments(p.oid)||')' AS name,
    jsonb_build_object('definition',pg_get_functiondef(p.oid),'acl',p.proacl::text,'owner',p.proowner::regrole::text) AS definition
    FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname !~ '^pg_' AND p.prokind IN ('f','p')
    AND NOT EXISTS(SELECT 1 FROM pg_depend d WHERE d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.deptype='e')
    AND cardinality($1::text[])>0 ORDER BY 1 LIMIT 10001`],
]

export async function inspectBehavior(connectionString, schemas, role) {
  if (!Array.isArray(schemas) || !schemas.length || schemas.some(s => !/^[a-z][a-z0-9_]{0,62}$/.test(s)) || !/^[a-z][a-z0-9_]{0,62}$/.test(role)) throw new Error('Invalid behavior contract scope')
  const client = new pg.Client({ connectionString, statement_timeout: 5000, connectionTimeoutMillis: 5000 })
  await client.connect()
  try {
    await client.query('BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY')
    const owners = await client.query("SELECT nspname FROM pg_namespace WHERE nspname=ANY($1) AND pg_has_role(current_user,nspowner,'USAGE')", [schemas])
    if (owners.rows.length !== schemas.length) throw new Error('Behavior inspection requires the schema owner connection')
    const entries = []
    for (const [kind, sql] of queries) {
      const { rows } = await client.query(sql, [schemas])
      if (rows.length > bound) throw new Error('Behavior contract exceeds catalog bounds')
      for (const row of rows) {
        if (Buffer.byteLength(JSON.stringify(row.definition)) > 1048576) throw new Error('Behavior definition exceeds bounds')
        entries.push({ id: `${kind}:${row.name}`, digest: hash(row.definition) })
      }
    }
    const roles = await client.query(`WITH RECURSIVE inherited(oid) AS (SELECT oid FROM pg_roles WHERE rolname=$1 UNION SELECT a.roleid FROM pg_auth_members a JOIN inherited i ON i.oid=a.member)
      SELECT r.rolname AS name,jsonb_build_object('superuser',r.rolsuper,'bypass_rls',r.rolbypassrls,'inherit',r.rolinherit,'settings',r.rolconfig,
        'database_settings',(SELECT setconfig FROM pg_db_role_setting WHERE setrole=r.oid AND setdatabase=(SELECT oid FROM pg_database WHERE datname=current_database())),
        'membership',coalesce((SELECT jsonb_agg(jsonb_build_array(a.roleid::regrole::text,a.admin_option,a.inherit_option,a.set_option) ORDER BY a.roleid::regrole::text) FROM pg_auth_members a WHERE a.member=r.oid),'[]'::jsonb)) AS definition
      FROM pg_roles r JOIN inherited i ON i.oid=r.oid ORDER BY 1 LIMIT 10001`, [role])
    if (!roles.rows.length || roles.rows.length > bound) throw new Error('Serving role unavailable or role graph exceeds bounds')
    for (const row of roles.rows) entries.push({ id: `role:${row.name}`, digest: hash(row.definition) })
    entries.sort((a,b) => a.id < b.id ? -1 : a.id > b.id ? 1 : 0)
    const snapshot = { version: 1, schemas: [...schemas].sort(), role, entries }
    const result = { ...snapshot, digest: hash(snapshot) }
    if (Buffer.byteLength(JSON.stringify(result)) > 1048576) throw new Error('Behavior contract exceeds output bounds')
    await client.query('COMMIT')
    return result
  } finally { await client.end() }
}

export function compareBehavior(before, after) {
  for (const value of [before, after]) {
    if (!value || value.version !== 1 || !Array.isArray(value.entries) || value.entries.length > bound * queries.length + bound || new Set(value.entries.map(e => e.id)).size !== value.entries.length || value.entries.some(e => typeof e.id !== 'string' || !/^[a-f0-9]{64}$/.test(e.digest))) throw new Error('Invalid behavior baseline')
    const { digest, ...snapshot } = value
    if (digest !== hash(snapshot)) throw new Error('Behavior baseline digest mismatch')
  }
  if (JSON.stringify(before.schemas) !== JSON.stringify(after.schemas) || before.role !== after.role) throw new Error('Behavior baseline scope differs')
  const previous = new Map(before.entries.map(e => [e.id, e.digest])), next = new Map(after.entries.map(e => [e.id, e.digest]))
  return [...new Set([...previous.keys(), ...next.keys()])].sort().filter(id => previous.get(id) !== next.get(id)).map(id => ({ id, change: !previous.has(id) ? 'added' : !next.has(id) ? 'removed' : 'changed' }))
}

export function requireBehaviorReplays(changes, coverage, passedScenarios) {
  if (!coverage || coverage.version !== 1 || !coverage.objects || typeof coverage.objects !== 'object' || Array.isArray(coverage.objects)) throw new Error('Configure version 1 behavior replay coverage')
  const passed = new Set(passedScenarios)
  for (const change of changes) {
    const names = coverage.objects[change.id] ?? coverage.objects[`${change.id.split(':')[0]}:*`] ?? coverage.objects['*']
    if (!Array.isArray(names) || !names.length || names.some(name => typeof name !== 'string' || !passed.has(name))) throw new Error(`Behavior change needs current successful authorization replays: ${change.id}`)
  }
}
