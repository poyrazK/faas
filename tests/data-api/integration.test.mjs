import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { writeFile, readFile, mkdtemp, cp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { createRequire } from 'node:module'
const runtime = process.env.DATA_API_RUNTIME_DIR ? pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/') : new URL('../../cmd/gregale/templates/data-api/',import.meta.url)
const runtimeRequire = createRequire(new URL('package.json',runtime))
const pg = runtimeRequire('pg')
const { generateKeyPair, exportJWK, createLocalJWKSet, SignJWT } = await import(pathToFileURL(runtimeRequire.resolve('jose')))
const { runtimeConfig } = await import(new URL('config.mjs',runtime))
const { createServer, tokenVerifier } = await import(new URL('server.mjs',runtime))
const { inspect, generate } = await import(new URL('types.mjs',runtime))
import { createDataClient } from '../../sdk/data/dist/index.js'
import { command } from './staging/canary.mjs'

const enabled = Boolean(process.env.DATA_API_TEST_DATABASE_URL && process.env.DATA_API_POSTGREST_BIN)
const repoRoot = new URL('../../', import.meta.url).pathname
async function stagingClient(t, types) {
  const directory = await mkdtemp(join(tmpdir(), 'data-api-staging-client-'))
  t.after(() => rm(directory, { recursive: true, force: true }))
  await writeFile(join(directory, 'package.json'), '{"type":"module"}')
  const receipt = JSON.parse(await command('npm', ['pack', '--json', '--pack-destination', directory], { cwd: join(repoRoot, 'sdk/data'), env: process.env }))
  // npm ci caches dependency tarballs, but a fresh client install also needs
  // registry metadata. Prefer the cache while allowing that first lookup.
  await command('npm', ['install', '--prefer-offline', '--ignore-scripts', '--no-audit', '--no-fund', join(directory, receipt[0].filename)], { cwd: directory, env: process.env })
  await writeFile(join(directory, 'database.types.ts'), types)
  await cp(new URL('./staging/client.ts', import.meta.url), join(directory, 'client.ts'))
  await command(join(repoRoot, 'sdk/data/node_modules/.bin/tsc'), ['--strict', '--skipLibCheck', '--target', 'ES2022', '--module', 'NodeNext', '--outDir', 'dist', 'client.ts'], { cwd: directory, env: process.env })
  return import(pathToFileURL(join(directory, 'dist/client.js')))
}
const port = async () => {
  const server = net.createServer()
  await new Promise(resolve => server.listen(0,'127.0.0.1',resolve))
  const value = server.address().port
  await new Promise(resolve => server.close(resolve))
  return value
}

test('real PostgREST, restricted SQL, JWT RLS, typed client and schema refresh', {skip: !enabled, timeout: 60000}, async t => {
  const suffix = `${process.pid}_${Date.now()}`
  const database = `data_api_${suffix}`
  const role = `data_api_login_${suffix}`
  const admin = new pg.Client({connectionString:process.env.DATA_API_TEST_DATABASE_URL})
  await admin.connect()
  let owner, child, server
  t.after(async () => {
    if (server) { server.closeAllConnections(); await new Promise(r=>server.close(r)) }
    if (child && child.exitCode === null) { child.kill(); await once(child,'exit') }
    await owner?.end()
    await admin.query(`DROP DATABASE IF EXISTS "${database}" WITH (FORCE)`)
    await admin.query(`DROP ROLE IF EXISTS "${role}"`)
    await admin.end()
  })
  await admin.query(`CREATE ROLE "${role}" LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD 'test-only'`)
  await admin.query(`CREATE DATABASE "${database}"`)
  const ownerURL = new URL(process.env.DATA_API_TEST_DATABASE_URL)
  ownerURL.pathname = `/${database}`
  owner = new pg.Client({connectionString:ownerURL.toString()})
  await owner.connect()
  await owner.query(`REVOKE ALL ON DATABASE "${database}" FROM PUBLIC;
    GRANT CONNECT ON DATABASE "${database}" TO "${role}";
    REVOKE ALL ON SCHEMA public FROM PUBLIC;
    CREATE SCHEMA api; CREATE TYPE api.state AS ENUM ('open','closed');
    CREATE DOMAIN api.note_body AS text NOT NULL;
    CREATE TABLE public.private_notes(secret text);
    CREATE TABLE api.notes(id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
      subject text NOT NULL, body text NOT NULL, state api.state, tags text[] NOT NULL DEFAULT '{}',
      payload jsonb, length integer GENERATED ALWAYS AS (length(body)) STORED);
    ALTER TABLE api.notes ENABLE ROW LEVEL SECURITY;
    CREATE POLICY own_notes ON api.notes USING(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub')
      WITH CHECK(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub');
    CREATE TABLE api.comments(id integer PRIMARY KEY, note_id bigint NOT NULL REFERENCES api.notes(id), body api.note_body);
    CREATE VIEW api.note_bodies WITH (security_invoker=true) AS SELECT id,body FROM api.notes;
    GRANT USAGE ON SCHEMA api TO "${role}";
    GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA api TO "${role}";
    GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA api TO "${role}";
    ALTER DEFAULT PRIVILEGES IN SCHEMA api GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO "${role}";
    CREATE FUNCTION api.echo(value text) RETURNS text LANGUAGE sql AS 'SELECT value'`)
  const loginURL = new URL(ownerURL)
  loginURL.username = role
  loginURL.password = 'test-only'
  const config = runtimeConfig({DATABASE_URL:loginURL.toString(),DATA_API_ISSUER:'https://issuer.example',DATA_API_JWKS_URL:'https://issuer.example/jwks',DATA_API_AUDIENCE:'notes',DATA_API_ALLOWED_ORIGINS:'https://app.example'})
  const upstream = await port(), ready = await port()
  let childLogs = ''
  child = spawn(process.env.DATA_API_POSTGREST_BIN, [], {env:{...process.env,...config.postgrestEnv,PGRST_SERVER_PORT:String(upstream),PGRST_ADMIN_SERVER_PORT:String(ready)},stdio:['ignore','pipe','pipe']})
  child.stdout.on('data',b=>{childLogs+=b}); child.stderr.on('data',b=>{childLogs+=b})
  const {privateKey,publicKey} = await generateKeyPair('ES256')
  const jwk = await exportJWK(publicKey)
  const sign = (subject, extra={}) => new SignJWT({sub:subject,role:'postgres',...extra}).setProtectedHeader({alg:'ES256'}).setIssuer(config.auth.issuer).setAudience(config.auth.audience).setExpirationTime('5m').sign(privateKey)
  server = createServer(config,tokenVerifier(config.auth,createLocalJWKSet({keys:[jwk]})),upstream,ready)
  await new Promise(r=>server.listen(0,'127.0.0.1',r))
  const base = `http://127.0.0.1:${server.address().port}`
  let available = false
  for (let attempt=0;attempt<100;attempt++) {
    if ((await fetch(base+'/healthz')).status===200) {available=true;break}
    if (child.exitCode !== null) break
    await new Promise(r=>setTimeout(r,50))
  }
  assert.equal(available,true,childLogs)
  const health = await fetch(base+'/healthz')
  assert.equal(health.headers.get('cache-control'), 'no-store')
  assert.deepEqual(await health.json(), { ready: true })
  const alice = await sign('alice'), bob = await sign('bob')
  assert.equal((await fetch(base+'/rest/v1/notes')).status,401)
  const wrongAudience = await new SignJWT({sub:'alice'}).setProtectedHeader({alg:'ES256'}).setIssuer(config.auth.issuer).setAudience('wrong').setExpirationTime('5m').sign(privateKey)
  assert.equal((await fetch(base+'/rest/v1/notes',{headers:{Authorization:`Bearer ${wrongAudience}`}})).status,401)
  const expired = await new SignJWT({sub:'alice'}).setProtectedHeader({alg:'ES256'}).setIssuer(config.auth.issuer).setAudience('notes').setExpirationTime(1).sign(privateKey)
  assert.equal((await fetch(base+'/rest/v1/notes',{headers:{Authorization:`Bearer ${expired}`}})).status,401)
  const client = token => createDataClient({url:base,accessToken:token}).schema('api')
  const added = await client(alice).from('notes').insert({subject:'alice',body:'hello',state:'open'}).select().single()
  assert.equal(added.error,null,JSON.stringify(added.error))
  assert.equal(added.data.length,5)
  assert.equal((await client(bob).from('notes').select()).data.length,0)
  assert.ok((await client(alice).from('notes').insert({subject:'bob',body:'forbidden'})).error)
  assert.equal((await client(bob).from('notes').update({body:'stolen'}).eq('id',added.data.id).select()).data.length,0)
  const page = await client(alice).from('notes').select('id,body',{count:'exact'}).eq('state','open').range(0,0)
  assert.equal(page.count,1)
  assert.equal(page.data[0].body,'hello')
  const comment = await client(alice).from('comments').insert({id:1,note_id:added.data.id,body:'reply'}).select('body,notes(body)').single()
  assert.equal(comment.error,null,JSON.stringify(comment.error))
  assert.equal(comment.data.notes.body,'hello')
  assert.equal((await client(bob).from('note_bodies').select()).data.length,0)
  assert.equal((await client(alice).from('private_notes').select()).status,404)
  const openapi = await (await fetch(base+'/openapi.json',{headers:{Authorization:`Bearer ${alice}`}})).json()
  assert.ok(openapi.paths['/notes'])
  assert.equal(openapi.host,undefined)
  assert.equal(openapi.basePath,'/rest/v1')
  assert.equal(openapi.paths['/private_notes'],undefined)
  assert.equal(openapi.paths['/rpc/echo'],undefined)
  assert.equal((await fetch(base+'/rest/v1/rpc/echo',{headers:{Authorization:`Bearer ${alice}`}})).status,404)
  const first = generate(await inspect(loginURL.toString(),['api']))
  const typedCanary = await stagingClient(t, first)
  const canaryID = await typedCanary.exercise(base, alice, bob)
  assert.equal((await client(alice).from('notes').delete().eq('id', canaryID)).error, null)
  assert.match(first, /"id"\?: never/)
  assert.match(first, /"length"\?: never/)
  assert.match(first, /"state"\?: "open" \| "closed" \| null/)
  assert.match(first,/"foreignKeyName":"comments_note_id_fkey"/)
  assert.doesNotMatch(first,/private_notes|test-only|data_api_login/)
  assert.equal(first,generate(await inspect(loginURL.toString(),['api'])))
  if (process.env.DATA_API_UPDATE_FIXTURE === '1') await writeFile(new URL('../../sdk/data/test/database.types.ts',import.meta.url),first)
  else assert.equal(first,await readFile(new URL('../../sdk/data/test/database.types.ts',import.meta.url),'utf8'))
  await owner.query('ALTER TABLE api.notes ADD COLUMN priority integer NOT NULL DEFAULT 0')
  const next = generate(await inspect(loginURL.toString(),['api']))
  assert.notEqual(next,first)
  assert.match(next,/"priority"\?: number/)
  child.kill('SIGUSR1')
  let refreshed = false
  for(let i=0;i<100;i++) {
    const result = await client(alice).from('notes').select('priority')
    if(result.data?.[0]?.priority===0) {refreshed=true;break}
    await new Promise(r=>setTimeout(r,50))
  }
  assert.equal(refreshed,true,childLogs)
  assert.equal((await client(alice).from('comments').delete().eq('id',1)).error,null)
  assert.equal((await client(alice).from('notes').delete().eq('id',added.data.id)).error,null)
  assert.equal((await client(alice).from('notes').select()).data.length,0)
})

test('staging release migrations replay and generate the typed client contract', {skip: !enabled, timeout: 60000}, async t => {
  const database = `staging_fixture_${process.pid}_${Date.now()}`
  const admin = new pg.Client({ connectionString: process.env.DATA_API_TEST_DATABASE_URL })
  await admin.connect()
  await admin.query(`CREATE DATABASE "${database}"`)
  const directory = await mkdtemp(join(tmpdir(), 'data-api-staging-migration-'))
  t.after(async () => {
    await admin.query(`DROP DATABASE IF EXISTS "${database}" WITH (FORCE)`)
    await admin.end()
    await rm(directory, { recursive: true, force: true })
  })
  const url = new URL(process.env.DATA_API_TEST_DATABASE_URL); url.pathname = `/${database}`
  await cp(new URL('./staging/migrate.mjs', import.meta.url), join(directory, 'migrate.mjs'))
  // Install the same locked dependencies used by the deployed migration app.
  for (const file of ['package.json', 'package-lock.json']) await cp(new URL(file, runtime), join(directory, file))
  await command('npm', ['ci', '--offline', '--ignore-scripts', '--no-audit', '--no-fund'], { cwd: directory, env: process.env })
  const migrate = () => command(process.execPath, [join(directory, 'migrate.mjs')], { cwd: directory, env: { ...process.env, MIGRATION_DATABASE_URL: url.toString() } })
  await writeFile(join(directory, 'version'), '1')
  await migrate(); await migrate()
  const before = generate(await inspect(url.toString(), ['api']))
  await stagingClient(t, before)
  assert.doesNotMatch(before, /"priority"/)
  await writeFile(join(directory, 'version'), '2')
  await migrate(); await migrate()
  const after = generate(await inspect(url.toString(), ['api']))
  assert.match(after, /"priority"\?: number/)
  assert.notEqual(before, after)
  await stagingClient(t, after)
})
