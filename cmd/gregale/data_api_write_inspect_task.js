const { Client } = require(require.resolve('pg', { paths: ['/app', process.cwd()] }))
async function main() {
  const table = process.argv[1]
  if (!/^[A-Za-z_][A-Za-z0-9_]{0,62}$/.test(table || '') || !process.env.DATABASE_URL) throw Error('invalid input')
  const client = new Client({ connectionString: process.env.DATABASE_URL, connectionTimeoutMillis: 5000, query_timeout: 10000 })
  await client.connect()
  try {
    const stream = client.connection && client.connection.stream
    if (!stream || typeof stream.write !== 'function') throw Error('connection unavailable')
    const encrypted = stream.encrypted === true
    const transport = { encrypted, peer_authorized: encrypted && typeof stream.authorized === 'boolean' ? stream.authorized : null }
    await client.query('BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY')
    await client.query("SET LOCAL search_path = pg_catalog; SET LOCAL statement_timeout = '5s'; SET LOCAL lock_timeout = '1s'")
    const role = (await client.query(`SELECT current_user AS name,current_database() AS database,r.rolsuper AS superuser,r.rolbypassrls AS bypass_rls FROM pg_roles r WHERE r.rolname=current_user`)).rows[0]
    const relation = (await client.query(`SELECT c.relname AS name,c.relkind AS kind,c.relrowsecurity AS rls,c.relforcerowsecurity AS force_rls,
      pg_has_role(current_user,c.relowner,'USAGE') AS owner_member, row_security_active(c.oid) AS row_security_active,
      has_schema_privilege(current_user,n.oid,'USAGE') AS schema_usage,
      has_table_privilege(current_user,c.oid,'SELECT') AS can_select,has_table_privilege(current_user,c.oid,'INSERT') AS can_insert,
      has_table_privilege(current_user,c.oid,'UPDATE') AS can_update,has_table_privilege(current_user,c.oid,'DELETE') AS can_delete
      FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='api' AND c.relname=$1`, [table])).rows[0]
    if (!role || !relation || !['r', 'p'].includes(relation.kind)) throw Error('relation unavailable')
    const columns = (await client.query(`SELECT a.attname AS name,a.attgenerated<>'' AS generated,a.attidentity<>'' AS identity,
      has_column_privilege(current_user,a.attrelid,a.attnum,'SELECT') AS can_select,
      has_column_privilege(current_user,a.attrelid,a.attnum,'INSERT') AS can_insert,
      has_column_privilege(current_user,a.attrelid,a.attnum,'UPDATE') AS can_update
      FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace
      WHERE n.nspname='api' AND c.relname=$1 AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum LIMIT 129`, [table])).rows
    const policies = (await client.query(`SELECT p.polname AS name,p.polcmd AS command,p.polpermissive AS permissive,
      EXISTS(SELECT 1 FROM unnest(p.polroles) x WHERE CASE WHEN x=0 THEN true ELSE pg_has_role(current_user,x,'USAGE') END) AS applies_to_role,
      p.polqual IS NOT NULL AS has_using,p.polwithcheck IS NOT NULL AS has_check
      FROM pg_policy p JOIN pg_class c ON c.oid=p.polrelid JOIN pg_namespace n ON n.oid=c.relnamespace
      WHERE n.nspname='api' AND c.relname=$1 ORDER BY p.polname LIMIT 129`, [table])).rows
    const triggers = (await client.query(`SELECT t.tgname AS name,t.tgenabled AS enabled,t.tgtype AS type
      FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace
      WHERE n.nspname='api' AND c.relname=$1 AND NOT t.tgisinternal ORDER BY t.tgname LIMIT 129`, [table])).rows
    if (columns.length > 128 || policies.length > 128 || triggers.length > 128) throw Error('catalog limit exceeded')
    await client.query('ROLLBACK')
    process.stdout.write(JSON.stringify({version:1,role,relation,columns,policies,triggers,transport})+'\n')
  } finally { await client.end() }
}
main().catch(() => { process.stderr.write('Write inspection unavailable; check the selected binding, runtime and catalog limits.\n'); process.exitCode=1 })
