import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { createRequire } from 'node:module'
import { pathToFileURL } from 'node:url'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
const enabled = Boolean(process.env.DATA_API_TEST_DATABASE_URL && process.env.DATA_API_POSTGREST_BIN && process.env.DATA_API_RUNTIME_DIR)
async function probePort() {
  const server = net.createServer(); await new Promise(r => server.listen(0, '127.0.0.1', r))
  const value = server.address().port; await new Promise(r => server.close(r)); return value
}
// A probe port is closed before it is used, so the OS can hand the same port
// to the next probe; PostgREST then refuses an admin port equal to its server
// port. Never return a port twice in one test file.
const issuedPorts = new Set()
async function port() {
  for (;;) {
    const value = await probePort()
    if (!issuedPorts.has(value)) {
      issuedPorts.add(value)
      return value
    }
  }
}
async function eventually(check) {
  for (let i = 0; i < 100; i++) { if (await check()) return; await new Promise(r => setTimeout(r, 30)) }
  assert.fail('Timed out waiting for fixture transition')
}

test('durable refresh drains requests, survives engine replacement, preserves RLS and blocks stale runtimes', { skip: !enabled, timeout: 60000 }, async t => {
  const runtime = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/')
  const require = createRequire(new URL('package.json', runtime)), pg = require('pg')
  const jose = await import(pathToFileURL(require.resolve('jose')))
  const signing = await jose.generateKeyPair('RS256')
  const { runHostedReplay } = await import(new URL('hosted-replay.mjs', runtime))
  const { runtimeConfig } = await import(new URL('config.mjs', runtime))
  const { createServer } = await import(new URL('server.mjs', runtime))
  const { inspect, fingerprint } = await import(new URL('types.mjs', runtime))
  const { installRefreshBarrier, checkRefreshSetup, changeRefreshBarrier, refreshStatusReader, refreshTokenHash } = await import(new URL('refresh.mjs', runtime))
  const suffix = `${process.pid}_${Date.now()}`, database = `refresh_${suffix}`, role = `refresh_login_${suffix}`
  const admin = new pg.Client({ connectionString: process.env.DATA_API_TEST_DATABASE_URL }); await admin.connect()
  const checkerRole = `refresh_checker_${suffix}`
  let owner, control, reader, child, held, checker
  const servers = [], records = []
  t.after(async () => {
    for (const server of servers) { server.closeAllConnections(); await new Promise(r => server.close(r)) }
    if (child && child.exitCode === null) { child.kill(); await once(child, 'exit') }
    await held?.end(); await reader?.close(); await control?.end(); await owner?.end(); await checker?.end()
    await admin.query(`DROP DATABASE IF EXISTS "${database}" WITH (FORCE)`)
    await admin.query(`DROP ROLE IF EXISTS "${checkerRole}"`); await admin.query(`DROP ROLE IF EXISTS "${role}"`); await admin.end()
  })
  await admin.query(`CREATE ROLE "${role}" LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD 'test-only'`)
  await admin.query(`CREATE DATABASE "${database}"`)
  const ownerURL = new URL(process.env.DATA_API_TEST_DATABASE_URL); ownerURL.pathname = `/${database}`
  owner = new pg.Client({ connectionString: ownerURL.toString() }); await owner.connect()
  control = new pg.Client({ connectionString: ownerURL.toString(), application_name: 'gregale-refresh-control-test' }); await control.connect()
  await owner.query(`CREATE SCHEMA api; CREATE TABLE api.notes(id int PRIMARY KEY, subject text NOT NULL, body text NOT NULL);
    INSERT INTO api.notes VALUES (1,'alice','a'),(2,'bob','b'); ALTER TABLE api.notes ENABLE ROW LEVEL SECURITY;
    CREATE POLICY owned ON api.notes USING (subject = current_setting('request.jwt.claims',true)::jsonb->>'sub');
    GRANT USAGE ON SCHEMA api TO "${role}"; GRANT SELECT,INSERT,UPDATE,DELETE ON api.notes TO "${role}";`)
  await assert.rejects(checkRefreshSetup(owner, role))
  await installRefreshBarrier(owner, role)
  const installed = await checkRefreshSetup(owner, role)
  assert.equal(installed.ready, true); assert.equal(installed.paused, false)
  assert.equal(installed.fingerprint, installed.live_fingerprint)
  const unchanged = await owner.query('SELECT * FROM gregale_data_api.refresh_gate')
  await checkRefreshSetup(owner, role)
  assert.deepEqual((await owner.query('SELECT * FROM gregale_data_api.refresh_gate')).rows, unchanged.rows)
  await owner.query(`REVOKE EXECUTE ON FUNCTION gregale_data_api.check_request() FROM "${role}"`)
  await assert.rejects(checkRefreshSetup(owner, role), /hook and status-function/)
  await owner.query(`GRANT EXECUTE ON FUNCTION gregale_data_api.check_request() TO "${role}"`)
  await owner.query('ALTER FUNCTION gregale_data_api.check_request() SECURITY INVOKER')
  await assert.rejects(checkRefreshSetup(owner, role), /SECURITY DEFINER/)
  await owner.query('ALTER FUNCTION gregale_data_api.check_request() SECURITY DEFINER')
  await owner.query('ALTER FUNCTION gregale_data_api.refresh_status(text) RESET search_path')
  await assert.rejects(checkRefreshSetup(owner, role), /pg_catalog search path/)
  await owner.query('ALTER FUNCTION gregale_data_api.refresh_status(text) SET search_path=pg_catalog')
  await owner.query(`GRANT CREATE ON SCHEMA gregale_data_api TO "${role}"; ALTER FUNCTION gregale_data_api.check_request() OWNER TO "${role}"; REVOKE CREATE ON SCHEMA gregale_data_api FROM "${role}"`)
  await assert.rejects(checkRefreshSetup(owner, role), /protected ownership/)
  await owner.query(`ALTER FUNCTION gregale_data_api.check_request() OWNER TO CURRENT_USER; GRANT EXECUTE ON FUNCTION gregale_data_api.check_request() TO "${role}"`)
  await admin.query(`CREATE ROLE "${checkerRole}" LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD 'test-only'`)
  await owner.query(`GRANT USAGE ON SCHEMA gregale_data_api TO "${checkerRole}"; GRANT SELECT,UPDATE ON gregale_data_api.refresh_gate TO "${checkerRole}"`)
  const checkerURL = new URL(ownerURL); checkerURL.username = checkerRole; checkerURL.password = 'test-only'
  checker = new pg.Client({ connectionString: checkerURL.toString() }); await checker.connect()
  await assert.rejects(checkRefreshSetup(checker, role), /SET ROLE/)
  await owner.query(`GRANT "${role}" TO "${checkerRole}"`)
  assert.equal((await checkRefreshSetup(checker, role)).ready, true)
  await owner.query(`REVOKE UPDATE ON gregale_data_api.refresh_gate FROM "${checkerRole}"`)
  await assert.rejects(checkRefreshSetup(checker, role), /SELECT\/UPDATE privileges/)
  await owner.query(`ALTER ROLE "${role}" CREATEROLE`)
  await assert.rejects(checkRefreshSetup(owner,role), /Serving role must remain restricted/)
  await assert.rejects(installRefreshBarrier(owner,role), /Serving role must remain restricted/)
  await owner.query(`ALTER ROLE "${role}" NOCREATEROLE`)
  const loginURL = new URL(ownerURL); loginURL.username = role; loginURL.password = 'test-only'
  const env = { DATABASE_URL: loginURL.toString(), DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes', DATA_API_COORDINATED_REFRESH: '1' }
  assert.throws(() => runtimeConfig({ ...env, DATA_API_SCHEMAS: 'api,gregale_data_api' }))
  const config = runtimeConfig(env), upstream = await port(), ready = await port()
  config.snapshot = await inspect(config.connection, config.schemas); config.functions = config.snapshot.functions; config.fingerprint = fingerprint(config.snapshot)
  reader = refreshStatusReader(config.connection); config.refreshStatus = reader.read
  assert.equal(config.postgrestEnv.PGRST_DB_POOL, '1', 'control reader and engine stay within two connections')
  async function engine() {
    child = spawn(process.env.DATA_API_POSTGREST_BIN, [], { env: { ...process.env, ...config.postgrestEnv, PGRST_SERVER_PORT: String(upstream), PGRST_ADMIN_SERVER_PORT: String(ready) }, stdio: ['ignore', 'pipe', 'pipe'] })
    await eventually(async () => { try { return (await fetch(`http://127.0.0.1:${ready}/ready`)).status === 200 } catch { return false } })
  }
  await engine()
  async function gateway(candidate) {
    const server = createServer(candidate, async token => { const sub = ['alice','bob'].includes(token) ? token : (await jose.jwtVerify(token, signing.publicKey, { issuer: env.DATA_API_ISSUER, audience: env.DATA_API_AUDIENCE })).payload.sub; return { sub, exp: 9999999999, gregale_refresh_hash: refreshTokenHash('a'.repeat(64)), gregale_serving_fingerprint: 'b'.repeat(64) } }, upstream, ready, row => records.push(row))
    await new Promise(r => server.listen(0, '127.0.0.1', r)); servers.push(server)
    return `http://127.0.0.1:${server.address().port}`
  }
  const old = await gateway(config), token = 'a'.repeat(64), stranger = 'c'.repeat(64)
  // Complete each fixture request before replacing the engine or probing its
  // single-connection pool; receiving headers alone does not drain the body.
  const request = async (url, path='/rest/v1/notes?select=id', refresh, identity='alice', method='GET') => {
    const response = await fetch(url + path, { method, headers: { Authorization: `Bearer ${identity}`, ...(refresh ? { 'X-Gregale-Refresh-Token': refresh } : {}) } })
    const body = await response.arrayBuffer()
    return new Response(body,{status:response.status,statusText:response.statusText,headers:response.headers})
  }
  assert.deepEqual(await (await request(old)).json(), [{ id: 1 }])
  held = new pg.Client({ connectionString: config.connection }); await held.connect()
  for (const isolation of ['REPEATABLE READ','SERIALIZABLE']) {
    await held.query(`BEGIN ISOLATION LEVEL ${isolation} READ ONLY`)
    await assert.rejects(held.query('SELECT gregale_data_api.check_request()'), error => error.code === 'PT503' && error.message === 'data_api_refresh_isolation_unsupported')
    await held.query('ROLLBACK')
  }
  await held.query('BEGIN READ ONLY')
  await held.query("SELECT set_config('request.jwt.claims',$1,true)", [JSON.stringify({ sub:'alice',gregale_serving_fingerprint:config.fingerprint })])
  await held.query('SELECT gregale_data_api.check_request()')
  let paused = false
  const drain = changeRefreshBarrier(control, 'pause', role, token).then(() => { paused = true })
  await eventually(async () => Number((await owner.query("SELECT count(*) FROM pg_stat_activity WHERE application_name='gregale-refresh-control-test' AND wait_event='advisory'")).rows[0].count) === 1)
  assert.equal(paused, false, 'pause must wait for admitted transactions')
  const queued = request(old)
  await eventually(async () => Number((await owner.query("SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory'")).rows[0].count) >= 2)
  await held.query('COMMIT'); await drain
  assert.equal((await checkRefreshSetup(owner, role)).paused, true)
  assert.equal((await queued).status, 503, 'queued requests must see the pause before application SQL')
  await held.end(); held = null
  assert.equal((await request(old)).status, 503, 'application JWT cannot forge a verification bypass')
  assert.equal((await request(old,undefined,stranger)).status, 503)
  assert.equal((await request(old,undefined,token,'bob')).status, 200)
  assert.deepEqual(await (await request(old,undefined,token,'bob')).json(), [{ id: 2 }], 'verification retains RLS')
  assert.equal((await fetch(old+'/rest/v1/notes', { headers: { 'X-Gregale-Refresh-Token':token } })).status, 401)
  await assert.rejects(changeRefreshBarrier(control,'pause',role,stranger))
  await assert.rejects(changeRefreshBarrier(control,'resume',role,stranger,config.fingerprint))
  const oldSchemaResponse = await request(old,'/__gregale/schema',token)
  const oldSchema = await oldSchemaResponse.json()
  assert.equal(oldSchema.refresh_verified,true,JSON.stringify({status:oldSchemaResponse.status,contract:oldSchema}))
  assert.equal((await request(old,'/__gregale/schema').then(r => r.json())).refresh_verified,false)
  await assert.rejects(owner.query('SET ROLE "'+role+'"; SELECT * FROM gregale_data_api.refresh_gate'))
  await owner.query('RESET ROLE')
  await owner.query('ALTER TABLE api.notes ADD COLUMN priority integer NOT NULL DEFAULT 0')
  child.kill(); await once(child,'exit'); await engine()
  const freshConfig = { ...config, snapshot:await inspect(config.connection,config.schemas) }
  freshConfig.fingerprint = fingerprint(freshConfig.snapshot); freshConfig.functions = freshConfig.snapshot.functions
  assert.notEqual(freshConfig.fingerprint,config.fingerprint)
  const fresh = await gateway(freshConfig)
  assert.equal((await request(fresh)).status,503,'pause survives engine and gateway replacement')
  assert.equal((await request(fresh,undefined,token)).status,200)
  const replayRoot = await mkdtemp(join(tmpdir(),'gregale-refresh-replay-'))
  t.after(() => rm(replayRoot,{recursive:true,force:true}))
  const collection = join(replayRoot,'requests.json')
  const scenarios = {version:2,scenarios:[{name:'notes-isolation',requests:[
    {name:'Alice sees owned row',method:'GET',path:'/rest/v1/notes?select=id&order=id',identity:'alice',expect:{status:200,json:[{id:1}]}},
    {name:'Bob cannot see Alice row',method:'GET',path:'/rest/v1/notes?id=eq.1&select=id',identity:'bob',expect:{status:200,json:[]}},
  ]}]}
  await writeFile(collection,JSON.stringify(scenarios))
  const jwt = subject => new jose.SignJWT({}).setProtectedHeader({alg:'RS256'}).setSubject(subject).setIssuer(env.DATA_API_ISSUER).setAudience(env.DATA_API_AUDIENCE).setExpirationTime('5m').sign(signing.privateKey)
  const options = {collection,identities:{alice:'ALICE_JWT',bob:'BOB_JWT'},scenarios:['notes-isolation'],url:'https://hosted-test.example/',fingerprint:freshConfig.fingerprint,require_paused:true}
  const replayEnv = {ALICE_JWT:await jwt('alice'),BOB_JWT:await jwt('bob'),GREGALE_DATA_API_REFRESH_TOKEN:token}
  const events = []
  // Adapt only the fixture transport; the hosted entrypoint accepts HTTPS origins.
  const fetcher = async (target,init) => {
    // Readiness can regress while PostgREST returns its sole connection.
    // Establish this transport prerequisite for each request, without retrying
    // an authentication/RLS assertion or changing the two-connection budget.
    let readyObservations = 0
    await eventually(async () => {
      const response = await fetch(`http://127.0.0.1:${ready}/ready`)
      await response.arrayBuffer()
      readyObservations = response.status === 200 ? readyObservations + 1 : 0
      return readyObservations >= 3
    })
    const response = await fetch(fresh + new URL(target).pathname + new URL(target).search,init)
    const body = await response.arrayBuffer()
    return new Response(body,{status:response.status,statusText:response.statusText,headers:response.headers})
  }
  // The deliberate paused (PT503) request triggers PostgREST's debounced
  // connection/schema-cache recovery. A single ready response can precede
  // that reload. Require a stable ready window before the hosted replay;
  // authentication and RLS assertions themselves are never retried.
  let readySince
  await eventually(async () => {
    const response = await request(fresh,'/__gregale/schema',token)
    const contract = await response.json()
    const ready = response.status === 200 && contract.ready === true && contract.fingerprint === freshConfig.fingerprint && contract.refresh_verified === true
    if (!ready) { readySince = undefined; return false }
    readySince ??= performance.now()
    return performance.now() - readySince >= 300
  })
  try {
    assert.equal((await runHostedReplay(options,replayEnv,{fetcher,emit:event=>events.push(event)})).failed,0)
  } catch (error) {
    // Emit only the hosted entrypoint's redacted assertion results.
    error.message += ': ' + JSON.stringify(events.filter(event => event.event === 'replay_result' && !event.passed))
    throw error
  }
  scenarios.scenarios[0].requests[1].expect.json=[{id:1}]
  await writeFile(collection,JSON.stringify(scenarios))
  await assert.rejects(runHostedReplay(options,replayEnv,{fetcher,emit:()=>{}}))
  assert.equal((await request(fresh)).status,503,'failed replay must not release the owner barrier')
  await assert.rejects(runHostedReplay(options,{...replayEnv,GREGALE_DATA_API_REFRESH_TOKEN:stranger},{fetcher,emit:()=>{}}))
  for (const credential of [token,replayEnv.ALICE_JWT,replayEnv.BOB_JWT]) assert.equal(JSON.stringify(events).includes(credential),false)
  await assert.rejects(changeRefreshBarrier(control,'resume',role,token,config.fingerprint), 'release must re-inspect the role-filtered live schema')
  assert.equal((await request(fresh)).status,503)
  await changeRefreshBarrier(control,'resume',role,token,freshConfig.fingerprint)
  assert.equal((await request(fresh)).status,200)
  assert.equal((await request(old)).status,409,'stale runtime cannot execute SQL after resume')
  assert.equal((await request(old,undefined,token)).status,409,'old token cannot bypass active fingerprint enforcement')
  await assert.rejects(changeRefreshBarrier(control,'resume',role,token,freshConfig.fingerprint),'cannot release an already released session')
  await changeRefreshBarrier(control,'pause',role,stranger)
  await owner.query('DELETE FROM gregale_data_api.refresh_gate')
  assert.equal((await request(fresh)).status,503,'missing state fails closed')
  assert.equal((await fetch(fresh+'/healthz')).status,503)
  const serialized = JSON.stringify(records)
  for (const sensitive of [token,stranger,refreshTokenHash(token),role]) assert.equal(serialized.includes(sensitive),false,'logs must not record refresh credentials or binding identities')
})
