import test from 'node:test'
import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { mkdtemp, writeFile, readFile, mkdir, symlink, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { createRequire } from 'node:module'
import { command } from './staging/canary.mjs'

const enabled = Boolean(process.env.DATA_API_TEST_DATABASE_URL && process.env.DATA_API_RUNTIME_DIR && process.env.DATA_API_DEV_CLI)

async function runCheck(cli, args) {
  const child = spawn(cli, ['--json', 'data-api', 'check', ...args], { stdio: ['ignore', 'pipe', 'pipe'] })
  let output = '', diagnostic = ''
  child.stdout.on('data', bytes => { output += bytes })
  child.stderr.on('data', bytes => { diagnostic += bytes })
  const [code] = await once(child, 'exit')
  assert.ok(output, diagnostic)
  return { code, report: JSON.parse(output) }
}

const originalFunction = `CREATE FUNCTION api.echo(value text, suffix text DEFAULT 'legacy') RETURNS text
  LANGUAGE sql AS $$ SELECT coalesce(value,'empty') || suffix $$`
const replacement = (args, returns = 'text', body = "SELECT coalesce(value::text,'empty') || suffix") =>
  `DROP FUNCTION api.echo(text,text); CREATE FUNCTION api.echo(${args}) RETURNS ${returns} LANGUAGE sql AS $$ ${body} $$`
const removed = [{ path: 'api.echo', reason: 'RPC removed; update calls', breaking: true }]
const change = (path, reason, breaking = true) => ({ path, reason, breaking })
const cases = [
  { name: 'unchanged', sql: '', changes: [] },
  { name: 'optional argument added', sql: replacement("value text,suffix text DEFAULT 'legacy',mode text DEFAULT ''"), changes: [change('api.echo(mode)', 'RPC argument added', false)] },
  { name: 'required argument added', sql: replacement("value text,extra integer,suffix text DEFAULT 'legacy'"), changes: [change('api.echo(extra)', 'RPC argument added')] },
  { name: 'argument removed', sql: replacement('value text', 'text', 'SELECT value'), changes: [change('api.echo(suffix)', 'RPC argument removed')] },
  { name: 'argument renamed', sql: replacement("body text,suffix text DEFAULT 'legacy'", 'text', 'SELECT body || suffix'), changes: [change('api.echo(value)', 'RPC argument removed'), change('api.echo(body)', 'RPC argument added')] },
  { name: 'argument type changed', sql: replacement("value integer,suffix text DEFAULT 'legacy'"), changes: [change('api.echo(value)', 'RPC argument type changed')] },
  { name: 'default removed', sql: replacement('value text,suffix text'), changes: [change('api.echo(suffix)', 'RPC argument optionality changed')] },
  { name: 'default added', sql: replacement("value text DEFAULT 'base',suffix text DEFAULT 'legacy'"), changes: [change('api.echo(value)', 'RPC argument optionality changed', false)] },
  { name: 'default value changed', sql: replacement("value text,suffix text DEFAULT 'revised'"), changes: [], actual: 'xrevised' },
  { name: 'nullable default changed', sql: replacement('value text,suffix text DEFAULT NULL'), changes: [], actual: null },
  { name: 'return type changed', sql: replacement("value text,suffix text DEFAULT 'legacy'", 'integer', 'SELECT length(value)'), changes: [change('api.echo', 'RPC return type changed')] },
  { name: 'set returning result changed', sql: replacement("value text,suffix text DEFAULT 'legacy'", 'SETOF text'), changes: [change('api.echo', 'RPC return type changed')] },
  { name: 'approval annotation removed', sql: 'COMMENT ON FUNCTION api.echo(text,text) IS NULL', changes: removed },
  { name: 'execute permission revoked', sql: 'REVOKE EXECUTE ON FUNCTION api.echo(text,text) FROM ROLE_PLACEHOLDER', changes: removed },
  { name: 'unapproved overload introduced', sql: "CREATE FUNCTION api.echo(value integer) RETURNS integer LANGUAGE sql AS 'SELECT value'; REVOKE EXECUTE ON FUNCTION api.echo(integer) FROM PUBLIC", changes: removed },
]

const sorted = changes => changes.map(({ path, reason, breaking }) => ({ path, reason, breaking })).sort((a, b) => (a.path + a.reason).localeCompare(b.path + b.reason))

// Every catalog change also compiles a real TypeScript client and exercises the
// CLI, so budget the complete matrix separately from individual HTTP deadlines.
test('RPC schema drift CI compares real catalog snapshots and never waives stale types', { skip: !enabled, timeout: 180000 }, async t => {
  const runtime = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/')
  const require = createRequire(new URL('package.json', runtime)), pg = require('pg')
  const { inspect, generate, fingerprint } = await import(new URL('types.mjs', runtime))
  const suffix = `${process.pid}_${Date.now()}`, database = `rpc_drift_${suffix}`, role = `rpc_drift_login_${suffix}`
  const admin = new pg.Client({ connectionString: process.env.DATA_API_TEST_DATABASE_URL })
  await admin.connect()
  const directory = await mkdtemp(join(tmpdir(), 'gregale-rpc-drift-'))
  let owner
  t.after(async () => {
    await owner?.end()
    await admin.query(`DROP DATABASE IF EXISTS "${database}" WITH (FORCE)`)
    await admin.query(`DROP ROLE IF EXISTS "${role}"`)
    await admin.end(); await rm(directory, { recursive: true, force: true })
  })
  await admin.query(`CREATE ROLE "${role}" LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD 'test-only'`)
  await admin.query(`CREATE DATABASE "${database}"`)
  const ownerURL = new URL(process.env.DATA_API_TEST_DATABASE_URL); ownerURL.pathname = `/${database}`
  owner = new pg.Client({ connectionString: ownerURL.toString() }); await owner.connect()
  await owner.query('REVOKE ALL ON SCHEMA public FROM PUBLIC')
  const loginURL = new URL(ownerURL); loginURL.username = role; loginURL.password = 'test-only'
  await mkdir(join(directory, 'node_modules/@gregale'), { recursive: true })
  await symlink(new URL('../../sdk/data', import.meta.url).pathname, join(directory, 'node_modules/@gregale/data'))
  await writeFile(join(directory, 'package.json'), '{"type":"module"}')
  await writeFile(join(directory, 'client.ts'), `import { createDataClient, validatedRpc } from '@gregale/data'
import type { Database } from './database.types.js'
const db = createDataClient<Database>({url:'https://data.example',accessToken:'token'}).schema('api')
type Args = Database['api']['Functions']['echo']['Args']
const nullableArgs: Args = {value:null,suffix:null}
const query = db.rpc('echo', {value:'x'})
const result = await validatedRpc({}, {parse:()=>nullableArgs}, args=>db.rpc('echo', args).retry(false))
if (result.outcome === 'completed') { const value: string | null = result.result.data; void value }
void query
`)
  const compile = () => command(new URL('../../sdk/data/node_modules/.bin/tsc', import.meta.url).pathname,
    ['--strict', '--noEmit', '--target', 'ES2022', '--module', 'NodeNext', 'client.ts'], { cwd: directory, env: process.env })
  const baselinePath = join(directory, 'baseline.json'), candidatePath = join(directory, 'candidate.json'), typePath = join(directory, 'database.types.ts'), approvalPath = join(directory, 'approval.json')
  const envelope = snapshot => JSON.stringify({ version: 1, fingerprint: fingerprint(snapshot), snapshot })
  for (const scenario of cases) {
    await t.test(scenario.name, async () => {
      await owner.query(`DROP SCHEMA IF EXISTS api CASCADE; CREATE SCHEMA api;
        GRANT USAGE ON SCHEMA api TO "${role}"; ${originalFunction};
        REVOKE EXECUTE ON FUNCTION api.echo(text,text) FROM PUBLIC;
        GRANT EXECUTE ON FUNCTION api.echo(text,text) TO "${role}";
        COMMENT ON FUNCTION api.echo(text,text) IS '@gregale:rpc'`)
      const before = await inspect(loginURL.toString(), ['api'])
      assert.equal(before.functions.length, 1)
      assert.deepEqual(before.functions[0].args, [{ name: 'value', type: 'string', optional: false }, { name: 'suffix', type: 'string', optional: true }])
      assert.equal(before.functions[0].returns, 'string | null')
      const originalTypes = generate(before)
      assert.match(originalTypes, /"value": string \| null/)
      assert.match(originalTypes, /"suffix"\?: string \| null/)
      await writeFile(baselinePath, envelope(before)); await writeFile(typePath, originalTypes)
      // All baseline calls, including explicit nullable arguments, compile.
      if (scenario.name === 'unchanged') {
        await compile()
        assert.equal((await owner.query('SELECT api.echo(NULL,NULL) AS value')).rows[0].value, null)
        assert.equal((await owner.query("SELECT api.echo('x') AS value")).rows[0].value, 'xlegacy')
      }
      if (scenario.sql) await owner.query(scenario.sql.replace('ROLE_PLACEHOLDER', `"${role}"`))
      // Replacement functions require renewed, explicit approval and execution grants.
      if (scenario.sql.startsWith('DROP FUNCTION')) {
        await owner.query(`REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA api FROM PUBLIC;
          GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA api TO "${role}"`)
        const { rows } = await owner.query("SELECT oid::regprocedure::text AS signature FROM pg_proc WHERE pronamespace='api'::regnamespace AND proname='echo'")
        assert.equal(rows.length, 1)
        await owner.query(`COMMENT ON FUNCTION ${rows[0].signature} IS '@gregale:rpc'`)
      }
      const after = await inspect(loginURL.toString(), ['api'])
      await writeFile(candidatePath, envelope(after))
      const args = ['--baseline', baselinePath, '--current', candidatePath, '--types', typePath]
      const drifting = fingerprint(before) !== fingerprint(after)
      const breaking = scenario.changes.filter(change => change.breaking).length
      const stale = await runCheck(process.env.DATA_API_DEV_CLI, args)
      assert.equal(stale.code, drifting ? 1 : 0)
      assert.equal(stale.report.passed, !drifting)
      assert.equal(stale.report.drift, drifting)
      assert.equal(stale.report.types.status, drifting ? 'stale' : 'current')
      assert.equal(stale.report.breaking, breaking)
      assert.equal(stale.report.unapproved, breaking)
      assert.deepEqual(sorted(stale.report.changes), sorted(scenario.changes))
      assert.equal(await readFile(typePath, 'utf8'), originalTypes, 'check must not update committed types')
      if (Object.hasOwn(scenario, 'actual')) assert.equal((await owner.query("SELECT api.echo('x') AS value")).rows[0].value, scenario.actual)
      const freshTypes = generate(after)
      await writeFile(typePath, freshTypes)
      const fresh = await runCheck(process.env.DATA_API_DEV_CLI, args)
      assert.equal(fresh.code, breaking ? 1 : 0)
      assert.equal(fresh.report.types.status, 'current')
      assert.equal(fresh.report.passed, breaking === 0)
      assert.deepEqual(sorted(fresh.report.changes), sorted(scenario.changes))
      if (breaking) await assert.rejects(compile(), error => error.code === 'command_failed')
      else await compile()
      if (breaking) {
        await writeFile(approvalPath, JSON.stringify({ version: 1, baseline_fingerprint: fingerprint(before), current_fingerprint: fingerprint(after), changes: scenario.changes.filter(change => change.breaking).map(({ path, reason }) => ({ path, reason, rationale: 'Disposable fixture client migration reviewed' })) }))
        await writeFile(typePath, originalTypes)
        const approvedStale = await runCheck(process.env.DATA_API_DEV_CLI, [...args, '--approval', approvalPath])
        assert.equal(approvedStale.code, 1); assert.equal(approvedStale.report.passed, false)
        assert.equal(approvedStale.report.types.status, 'stale'); assert.equal(approvedStale.report.unapproved, 0)
        assert.equal(approvedStale.report.approved, breaking)
        await writeFile(typePath, freshTypes)
        const approvedFresh = await runCheck(process.env.DATA_API_DEV_CLI, [...args, '--approval', approvalPath])
        assert.equal(approvedFresh.code, 0); assert.equal(approvedFresh.report.passed, true)
        assert.equal(approvedFresh.report.types.status, 'current'); assert.equal(approvedFresh.report.approved, breaking)
      }
      assert.equal(await readFile(typePath, 'utf8'), freshTypes)
      assert.equal(await readFile(baselinePath, 'utf8'), envelope(before))
      assert.equal(await readFile(candidatePath, 'utf8'), envelope(after))
    })
  }
})
