import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import { spawn, execFileSync } from 'node:child_process'
import { once } from 'node:events'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { pathToFileURL, fileURLToPath } from 'node:url'
import { createRequire } from 'node:module'
const enabled = Boolean(process.env.DATA_API_TEST_DATABASE_URL && process.env.DATA_API_POSTGREST_BIN && process.env.DATA_API_RUNTIME_DIR)

// A probe port is closed before it is used, so the OS can hand the same port
// to the next probe; PostgREST then refuses an admin port equal to its server
// port. Never return a port twice in one test file.
const issuedPorts = new Set()
async function port() {
  for (;;) {
    const server = net.createServer()
    await new Promise(r => server.listen(0, '127.0.0.1', r))
    const value = server.address().port
    await new Promise(r => server.close(r))
    if (!issuedPorts.has(value)) {
      issuedPorts.add(value)
      return value
    }
  }
}

test('restricted composite contracts match PostgreSQL, typed CRUD/RPC and breaking nested changes', { skip: !enabled, timeout: 60000 }, async t => {
  const runtime = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/')
  const require = createRequire(new URL('package.json', runtime))
  const pg = require('pg')
  const { generateKeyPair, exportJWK, createLocalJWKSet, SignJWT } = await import(pathToFileURL(require.resolve('jose')))
  const { runtimeConfig } = await import(new URL('config.mjs', runtime))
  const { inspect, inspectClient, generate, fingerprint } = await import(new URL('types.mjs', runtime))
  const { createServer, tokenVerifier } = await import(new URL('server.mjs', runtime))
  const suffix = `${process.pid}_${Date.now()}`, database = `composites_${suffix}`, role = `composites_login_${suffix}`
  const admin = new pg.Client({ connectionString: process.env.DATA_API_TEST_DATABASE_URL })
  await admin.connect()
  let owner, child, gateway
  const directory = await mkdtemp(join(tmpdir(), 'gregale-composites-'))
  t.after(async () => {
    if (gateway) { gateway.closeAllConnections(); await new Promise(r => gateway.close(r)) }
    if (child && child.exitCode === null) { child.kill(); await once(child, 'exit') }
    await owner?.end()
    await admin.query(`DROP DATABASE IF EXISTS "${database}" WITH (FORCE)`)
    await admin.query(`DROP ROLE IF EXISTS "${role}"`)
    await admin.end(); await rm(directory, { recursive: true, force: true })
  })
  await admin.query(`CREATE ROLE "${role}" LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD 'test-only'`)
  await admin.query(`CREATE DATABASE "${database}"`)
  const ownerURL = new URL(process.env.DATA_API_TEST_DATABASE_URL); ownerURL.pathname = `/${database}`
  owner = new pg.Client({ connectionString: ownerURL.toString() }); await owner.connect()
  await owner.query(`REVOKE ALL ON SCHEMA public FROM PUBLIC; CREATE SCHEMA api; CREATE SCHEMA private;
    CREATE DOMAIN api.required_label AS text NOT NULL;
    CREATE TYPE api.mood AS ENUM ('happy','quiet');
    CREATE TYPE api.location AS (x integer, label api.required_label, tags text[]);
    CREATE TYPE api.profile AS (display_name text, home api.location, previous api.location[], mood api.mood);
    CREATE DOMAIN api.profile_domain AS api.profile;
    CREATE TABLE api.profiles(id integer PRIMARY KEY, subject text NOT NULL, profile api.profile);
    ALTER TABLE api.profiles ENABLE ROW LEVEL SECURITY;
    CREATE POLICY own_profile ON api.profiles USING(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub') WITH CHECK(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub');
    GRANT USAGE ON SCHEMA api TO "${role}";
    GRANT SELECT,INSERT,UPDATE,DELETE ON api.profiles TO "${role}";
    CREATE TYPE api.hidden AS (secret text); REVOKE ALL ON TYPE api.hidden FROM PUBLIC;
    CREATE TYPE private.secret AS (hidden_value text);
    CREATE TABLE api.private_rows(hidden_column text);
    CREATE TYPE api.opaque AS (a api.hidden, b private.secret, c api.private_rows);
    CREATE FUNCTION api.read_profile() RETURNS api.profile LANGUAGE sql AS 'SELECT profile FROM api.profiles';
    CREATE FUNCTION api.list_profiles() RETURNS SETOF api.profile LANGUAGE sql AS 'SELECT profile FROM api.profiles';
    CREATE FUNCTION api.echo_profile(value api.profile) RETURNS api.profile LANGUAGE sql AS 'SELECT value';
    CREATE FUNCTION api.domain_profile(value api.profile_domain) RETURNS api.profile_domain LANGUAGE sql AS 'SELECT value';
    CREATE FUNCTION api.profile_history(value api.location[]) RETURNS api.location[] LANGUAGE sql AS 'SELECT value';
    CREATE FUNCTION api.empty_location() RETURNS api.location LANGUAGE sql AS 'SELECT NULL::api.location';
    CREATE FUNCTION api.opaque_result() RETURNS api.opaque LANGUAGE sql AS 'SELECT NULL::api.opaque';
    CREATE FUNCTION api.definer_profile() RETURNS api.profile LANGUAGE sql SECURITY DEFINER AS 'SELECT NULL::api.profile';
    COMMENT ON FUNCTION api.read_profile() IS '@gregale:rpc';
    COMMENT ON FUNCTION api.list_profiles() IS '@gregale:rpc';
    COMMENT ON FUNCTION api.echo_profile(api.profile) IS '@gregale:rpc';
    COMMENT ON FUNCTION api.domain_profile(api.profile_domain) IS '@gregale:rpc';
    COMMENT ON FUNCTION api.profile_history(api.location[]) IS '@gregale:rpc';
    COMMENT ON FUNCTION api.empty_location() IS '@gregale:rpc';
    COMMENT ON FUNCTION api.opaque_result() IS '@gregale:rpc';
    COMMENT ON FUNCTION api.definer_profile() IS '@gregale:rpc'`)
  const loginURL = new URL(ownerURL); loginURL.username = role; loginURL.password = 'test-only'
  const snapshot = await inspect(loginURL.toString(), ['api'])
  const plan = await inspectClient(owner, ['api'], { includeUnexecutable: true, privilegeRole: role })
  assert.deepEqual(plan.functions.map(f => f.name), snapshot.functions.map(f => f.name), 'permission plans must use serving-role type access')
  assert.deepEqual(snapshot.functions.map(f => f.name), ['domain_profile', 'echo_profile', 'empty_location', 'list_profiles', 'profile_history', 'read_profile'])
  const types = generate(snapshot)
  assert.doesNotMatch(types, /hidden_value|hidden_column|"hidden"|"secret"|definer_profile|opaque_result/)
  assert.deepEqual(snapshot.composites.find(c => c.name === 'opaque').fields.map(f => f.type), ['unknown', 'unknown', 'unknown'])
  assert.equal(snapshot.composites.find(c => c.name === 'location').fields.find(f => f.name === 'label').nullable, true)
  const config = runtimeConfig({ DATABASE_URL: loginURL.toString(), DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'profiles' })
  config.snapshot = snapshot; config.functions = snapshot.functions
  const upstream = await port(), ready = await port()
  child = spawn(process.env.DATA_API_POSTGREST_BIN, [], { env: { ...process.env, ...config.postgrestEnv, PGRST_SERVER_PORT: String(upstream), PGRST_ADMIN_SERVER_PORT: String(ready) }, stdio: ['ignore', 'pipe', 'pipe'] })
  let logs = ''; child.stdout.on('data', b => { logs += b }); child.stderr.on('data', b => { logs += b })
  const { privateKey, publicKey } = await generateKeyPair('ES256'), jwk = await exportJWK(publicKey)
  const sign = sub => new SignJWT({ sub, role: 'postgres' }).setProtectedHeader({ alg: 'ES256' }).setIssuer(config.auth.issuer).setAudience(config.auth.audience).setExpirationTime('5m').sign(privateKey)
  gateway = createServer(config, tokenVerifier(config.auth, createLocalJWKSet({ keys: [jwk] })), upstream, ready, () => {})
  await new Promise(r => gateway.listen(0, '127.0.0.1', r))
  const url = `http://127.0.0.1:${gateway.address().port}`
  for (let i = 0; i < 100 && (await fetch(url + '/healthz')).status !== 200 && child.exitCode === null; i++) await new Promise(r => setTimeout(r, 50))
  assert.equal((await fetch(url + '/healthz')).status, 200, logs)
  await writeFile(join(directory, 'package.json'), '{"type":"module"}')
  await writeFile(join(directory, 'database.types.ts'), types)
  const sdk = fileURLToPath(new URL('../../sdk/data/dist/index.js', import.meta.url))
  await writeFile(join(directory, 'client.ts'), `import {createDataClient} from ${JSON.stringify(sdk)};
import type {Database,CompositeTypes} from './database.types.js';
export const profile: CompositeTypes<'profile'> = {display_name:'Alice',home:{x:1,label:'Home',tags:['one',null]},previous:[null,{x:null,label:'Old',tags:null}],mood:'happy'};
// @ts-expect-error Unknown composite names fail at compile time.
type Missing = CompositeTypes<'missing'>;
// @ts-expect-error Domain base types are preserved.
const invalid: CompositeTypes<'location'> = {x:1,label:123,tags:[]};
// @ts-expect-error Nested fields retain their database types.
const wrong: CompositeTypes<'profile'> = {...profile, home:{x:'bad',label:'Home',tags:[]}};
export async function exercise(url:string, token:string) {
 const db=createDataClient<Database>({url,accessToken:token}).schema('api');
 const insert=await db.from('profiles').insert({id:1,subject:'alice',profile}).select().single();
 const single=await db.rpc('read_profile');
 const set=await db.rpc('list_profiles');
 const echo=await db.rpc('echo_profile',{value:profile});
 const domain=await db.rpc('domain_profile',{value:profile});
 const array=await db.rpc('profile_history',{value:profile.previous});
 const empty=await db.rpc('empty_location');
 const x: number|null|undefined=single.data?.home?.x;
 const label: string|null|undefined=insert.data?.profile?.home?.label;
 const history: CompositeTypes<'location'>|null|undefined=set.data?.[0]?.previous?.[1];
 // @ts-expect-error RPC inputs use the composite contract.
 const invalidQuery=db.rpc('echo_profile',{value:{bad:true}});
 void [x,label,history,invalidQuery];
 return {insert,single,set,echo,domain,array,empty};
}`)
  const tsc = fileURLToPath(new URL('../../sdk/data/node_modules/typescript/bin/tsc', import.meta.url))
  execFileSync(process.execPath, [tsc, '--strict', '--skipLibCheck', '--target', 'ES2022', '--module', 'NodeNext', '--outDir', join(directory, 'dist'), join(directory, 'client.ts')], { encoding: 'utf8' })
  const client = await import(pathToFileURL(join(directory, 'dist/client.js')))
  const alice = await sign('alice'), bob = await sign('bob')
  const results = await client.exercise(url, alice)
  for (const result of Object.values(results)) assert.equal(result.error, null, JSON.stringify(result.error))
  assert.deepEqual(results.insert.data.profile, client.profile)
  for (const key of ['single', 'echo', 'domain']) assert.deepEqual(results[key].data, client.profile, key)
  assert.deepEqual(results.set.data, [client.profile])
  assert.deepEqual(results.array.data, client.profile.previous)
  assert.deepEqual(results.empty.data, { x: null, label: null, tags: null })
  const headers = { Authorization: `Bearer ${bob}`, 'Content-Type': 'application/json' }
  assert.deepEqual(await (await fetch(url + '/rest/v1/profiles', { headers })).json(), [])
  assert.deepEqual(await (await fetch(url + '/rest/v1/rpc/list_profiles', { method: 'POST', headers, body: '{}' })).json(), [])
  assert.deepEqual(await (await fetch(url + '/rest/v1/rpc/read_profile', { method: 'POST', headers, body: '{}' })).json(), { display_name: null, home: null, previous: null, mood: null })
  assert.equal((await fetch(url + '/rest/v1/rpc/opaque_result', { method: 'POST', headers, body: '{}' })).status, 404)
  assert.equal((await fetch(url + '/rest/v1/rpc/read_profile', { headers })).status, 405)
  assert.equal((await fetch(url + '/rest/v1/rpc/read_profile', { method: 'POST' })).status, 401)
  const spec = await (await fetch(url + '/openapi.json', { headers })).json()
  assert.ok(spec.definitions['api.profile.Composite'])
  assert.deepEqual(spec.definitions['api.profile.Composite'].properties.home, { $ref: '#/definitions/api.location.Composite', 'x-nullable': true })
  assert.equal(spec.paths['/opaque'], undefined)
  assert.equal(spec.paths['/rpc/opaque_result'], undefined)
  // A nested change must affect the fingerprint even when table/RPC references stay identical.
  await owner.query('ALTER TYPE api.location ADD ATTRIBUTE postcode text')
  const next = await inspect(loginURL.toString(), ['api'])
  assert.notEqual(fingerprint(snapshot), fingerprint(next))
  assert.equal(snapshot.functions[0].returns, next.functions[0].returns)
  if (process.env.DATA_API_DEV_CLI) {
    for (const [name, value] of [['before', snapshot], ['after', next]]) await writeFile(join(directory, name + '.json'), JSON.stringify({ version: 1, fingerprint: fingerprint(value), snapshot: value }))
    const diff = spawn(process.env.DATA_API_DEV_CLI, ['--json', 'data-api', 'diff', '--baseline', join(directory, 'before.json'), '--current', join(directory, 'after.json'), '--check'], { stdio: ['ignore', 'pipe', 'pipe'] })
    let output = ''; diff.stdout.on('data', b => { output += b }); diff.stderr.resume()
    const [code] = await once(diff, 'exit')
    assert.equal(code, 1, output)
    assert.ok(JSON.parse(output).changes.some(c => c.path === 'api.location.postcode' && c.breaking))
    // Use the actual restricted PostgreSQL snapshot and native generated types,
    // rather than fabricated type text, for the unified CI check.
    const check = async (current, expectedExit) => {
      const child = spawn(process.env.DATA_API_DEV_CLI, ['--json', 'data-api', 'check', '--baseline', join(directory, 'before.json'), '--current', join(directory, current + '.json'), '--types', join(directory, 'database.types.ts')], { stdio: ['ignore', 'pipe', 'pipe'] })
      let report = '', diagnostic = ''; child.stdout.on('data', b => { report += b }); child.stderr.on('data', b => { diagnostic += b })
      const [exit] = await once(child, 'exit')
      assert.equal(exit, expectedExit, diagnostic || report)
      return JSON.parse(report)
    }
    assert.equal((await check('before', 0)).types.status, 'current')
    const stale = await check('after', 1)
    assert.equal(stale.types.status, 'stale')
    assert.ok(stale.changes.some(c => c.path === 'api.location.postcode' && c.breaking))
    await writeFile(join(directory, 'database.types.ts'), generate(next))
    const updated = await check('after', 1)
    assert.equal(updated.types.status, 'current')
    assert.ok(updated.unapproved > 0, 'regenerated types must not waive a breaking composite change')

  }
})
