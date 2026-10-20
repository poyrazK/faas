import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import { readFile, writeFile, mkdir, mkdtemp, symlink, rm } from 'node:fs/promises'
import { spawn, spawnSync } from 'node:child_process'
import { once } from 'node:events'
import { pathToFileURL } from 'node:url'
import { createRequire } from 'node:module'
import { join } from 'node:path'
import { randomUUID } from 'node:crypto'
import { tmpdir } from 'node:os'
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

test('import previews are read-only, typed and tenant-scoped with atomic commit validation', { skip: !enabled, timeout: 60000 }, async t => {
  const runtime = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/')
  const require = createRequire(new URL('package.json', runtime)), pg = require('pg')
  const { generateKeyPair, exportJWK, createLocalJWKSet, SignJWT } = await import(pathToFileURL(require.resolve('jose')))
  const { runtimeConfig } = await import(new URL('config.mjs', runtime))
  const { inspect, fingerprint, generate } = await import(new URL('types.mjs', runtime))
  const { createServer, tokenVerifier } = await import(new URL('server.mjs', runtime))
  const suffix = `${process.pid}_${Date.now()}`, database = `cursor_${suffix}`, role = `cursor_login_${suffix}`
  const admin = new pg.Client({ connectionString: process.env.DATA_API_TEST_DATABASE_URL })
  await admin.connect()
  let owner, child, gateway
  t.after(async () => {
    if (gateway) { gateway.closeAllConnections(); await new Promise(r => gateway.close(r)) }
    if (child && child.exitCode === null) { child.kill(); await once(child, 'exit') }
    await owner?.end()
    await admin.query(`DROP DATABASE IF EXISTS "${database}" WITH (FORCE)`)
    await admin.query(`DROP ROLE IF EXISTS "${role}"`)
    await admin.end()
  })
  await admin.query(`CREATE ROLE "${role}" LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD 'test-only'`)
  await admin.query(`CREATE DATABASE "${database}"`)
  const ownerURL = new URL(process.env.DATA_API_TEST_DATABASE_URL); ownerURL.pathname = `/${database}`
  owner = new pg.Client({ connectionString: ownerURL.toString() }); await owner.connect()
  await owner.query(await readFile(new URL('../../examples/data-api-import-preview/migrations/0001_import.sql', import.meta.url), 'utf8'))
  await owner.query(await readFile(new URL('../../examples/data-api-import-preview/migrations/0002_review_versions.sql', import.meta.url), 'utf8'))
  await owner.query(`GRANT USAGE ON SCHEMA api TO "${role}";
    GRANT SELECT,INSERT,UPDATE,DELETE ON api.documents TO "${role}";
    GRANT USAGE ON ALL SEQUENCES IN SCHEMA api TO "${role}";
    GRANT EXECUTE ON FUNCTION api.preview_import(jsonb),api.commit_import(jsonb,jsonb,jsonb) TO "${role}"`)
  await owner.query("INSERT INTO api.documents(owner,tenant,external_key,body) VALUES('alice','team-a','existing','original'),('alice','team-b','hidden','secret')")
  const loginURL = new URL(ownerURL); loginURL.username = role; loginURL.password = 'test-only'
  const snapshot = await inspect(loginURL.toString(), ['api'])
  const typed = await mkdtemp(join(tmpdir(), 'optimistic-client-'))
  t.after(() => rm(typed, { recursive: true, force: true }))
  await mkdir(join(typed, 'node_modules/@gregale'), { recursive: true })
  await symlink(new URL('../../sdk/data', import.meta.url).pathname, join(typed, 'node_modules/@gregale/data'))
  await writeFile(join(typed, 'package.json'), '{"type":"module"}')
  await writeFile(join(typed, 'database.types.ts'), generate(snapshot))
  await writeFile(join(typed, 'client.ts'), await readFile(new URL('../../examples/data-api-import-preview/client.ts', import.meta.url), 'utf8'))
  const compiled = spawnSync(new URL('../../sdk/data/node_modules/.bin/tsc', import.meta.url).pathname, ['--outDir','dist','--strict','--target','ES2022','--module','NodeNext','client.ts'], { cwd: typed, env: process.env, encoding: 'utf8' })
  assert.equal(compiled.status, 0, compiled.stdout + compiled.stderr)

  const config = runtimeConfig({ DATABASE_URL: loginURL.toString(), DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'orders' })
  config.snapshot = snapshot; config.functions = snapshot.functions; config.fingerprint = fingerprint(snapshot)
  const upstream = await port(), ready = await port()
  child = spawn(process.env.DATA_API_POSTGREST_BIN, [], { env: { ...process.env, ...config.postgrestEnv, PGRST_SERVER_PORT: String(upstream), PGRST_ADMIN_SERVER_PORT: String(ready) }, stdio: ['ignore', 'pipe', 'pipe'] })
  let logs = ''; child.stdout.on('data', b => { logs += b }); child.stderr.on('data', b => { logs += b })
  const { privateKey, publicKey } = await generateKeyPair('ES256'), jwk = await exportJWK(publicKey)
  const sign = (sub, tenant = 'team-a', worker = false) => new SignJWT({ sub, tenant, outbox_worker: worker, role: 'postgres' }).setProtectedHeader({ alg: 'ES256' }).setIssuer(config.auth.issuer).setAudience(config.auth.audience).setExpirationTime('5m').sign(privateKey)
  gateway = createServer(config, tokenVerifier(config.auth, createLocalJWKSet({ keys: [jwk] })), upstream, ready, () => {})
  await new Promise(r => gateway.listen(0, '127.0.0.1', r))
  const url = `http://127.0.0.1:${gateway.address().port}`
  for (let i = 0; i < 100 && (await fetch(url + '/healthz')).status !== 200 && child.exitCode === null; i++) await new Promise(r => setTimeout(r, 50))
  assert.equal((await fetch(url + '/healthz')).status, 200, logs)
  const { createDataClient, dataRequest } = await import('../../sdk/data/dist/index.js')
  const alice = createDataClient({ url, accessToken: await sign('alice') }).schema('api')
  const otherTenant = createDataClient({ url, accessToken: await sign('alice', 'team-b') }).schema('api')




  const { previewImport, commitImport, parsePreview, approveImport }=await import(pathToFileURL(join(typed,'dist/client.js')))
  const token=await sign('alice')
  const input=[{key:'existing',body:'updated'},{key:'new',body:'draft'},{key:'duplicate',body:'a'},{key:'duplicate',body:'b'},{key:'empty',body:''},{key:'hidden',body:'mine'}]
  const before=await owner.query('SELECT * FROM api.documents ORDER BY id')
  const sequence=await owner.query('SELECT last_value,is_called FROM api.documents_id_seq')
  const preview=await previewImport(url,token,input)
  assert.equal(preview.total,6);assert.equal(preview.valid,3)
  assert.deepEqual(preview.rows.map(r=>r.action),['update','insert','invalid','invalid','invalid','insert'])
  assert.deepEqual(preview.rows[2].errors,['duplicate_input_key'])
  assert.deepEqual((await owner.query('SELECT * FROM api.documents ORDER BY id')).rows,before.rows)
  assert.deepEqual((await owner.query('SELECT last_value,is_called FROM api.documents_id_seq')).rows,sequence.rows)
  // STABLE preview executes inside a genuinely read-only, restricted transaction.
  await owner.query('BEGIN READ ONLY')
  await owner.query(`SET LOCAL ROLE "${role}"`)
  await owner.query("SELECT set_config('request.jwt.claims',$1,true)",[JSON.stringify({sub:'alice',tenant:'team-a'})])
  assert.equal((await owner.query('SELECT api.preview_import($1::jsonb) AS preview',[JSON.stringify(input)])).rows[0].preview.total,6)
  await owner.query('ROLLBACK')
  const foreign=await previewImport(url,await sign('alice','team-b'),[{key:'existing',body:'x'}])
  assert.equal(foreign.rows[0].action,'insert')
  const malformed=await dataRequest(alice.rpc('preview_import',{items:[null,3,{}, {key:'x',body:'valid',unknown:'field'}]}),{timeoutMs:5000})
  assert.equal(malformed.error,null);assert.ok(malformed.data.rows.every(r=>r.action==='invalid'))
  assert.equal((await dataRequest(alice.rpc('preview_import',{items:{}}),{timeoutMs:5000})).status,400)
  assert.equal((await dataRequest(alice.rpc('preview_import',{items:Array.from({length:101},()=>({key:'x',body:'a'}))}),{timeoutMs:5000})).status,400)
  assert.deepEqual(await previewImport(url,token,[]),{identity:{subject:'alice',tenant:'team-a'},items:[],rows:[],total:0,valid:0})
  assert.throws(()=>approveImport(preview),/validation errors/)
  const rejected=await dataRequest(alice.rpc('commit_import',{items:input,expected:input.map(()=>({id:null,version:null})),review_identity:{subject:'alice',tenant:'team-a'}}),{timeoutMs:5000})
  assert.equal(rejected.status,400)
  assert.deepEqual((await owner.query('SELECT * FROM api.documents ORDER BY id')).rows,before.rows)
  // State can change between preview and commit: commit validates fresh input.
  const valid=[{key:'new',body:'reviewed'}]
  const newApproval=approveImport(await previewImport(url,token,valid))
  assert.equal(newApproval.expected[0].id,null)
  await owner.query("INSERT INTO api.documents(owner,tenant,external_key,body) VALUES('alice','team-a','new','intervening')")
  const staleNew=await commitImport(url,token,newApproval)
  assert.equal(staleNew.status,409);assert.equal(staleNew.error.message,'import_review_conflict')
  assert.equal(JSON.parse(staleNew.error.details).index,0)
  const committed=await commitImport(url,token,approveImport(await previewImport(url,token,valid)))
  assert.equal(committed.error,null);assert.equal(committed.data[0].body,'reviewed')
  assert.equal((await previewImport(url,token,valid)).rows[0].action,'unchanged')
  assert.equal((await owner.query("SELECT body FROM api.documents WHERE tenant='team-b' AND external_key='hidden'")).rows[0].body,'secret')
  // A new database rule must still reject the whole commit after a valid preview.
  const later=[{key:'atomic-a',body:'good'},{key:'atomic-b',body:'blocked'}]
  const laterApproval=approveImport(await previewImport(url,token,later))
  await owner.query("ALTER TABLE api.documents ADD CONSTRAINT new_body_rule CHECK(body<>'blocked')")
  assert.equal((await commitImport(url,token,laterApproval)).error.code,'23514')
  assert.equal((await owner.query("SELECT count(*)::integer AS count FROM api.documents WHERE external_key LIKE 'atomic-%'")).rows[0].count,0)
  assert.throws(()=>parsePreview({total:1,valid:1,rows:[]}),/Invalid/)
  assert.throws(()=>parsePreview({total:1,valid:1,rows:[{index:0,action:'invalid',errors:[]}]}),/Invalid/)
  const edit=[{key:'existing',body:'reviewed edit'}]
  const editApproval=approveImport(await previewImport(url,token,edit))
  assert.equal(editApproval.expected[0].version,1)
  await owner.query("UPDATE api.documents SET body='concurrent edit',version=1 WHERE tenant='team-a' AND external_key='existing'")
  const conflict=await commitImport(url,token,editApproval)
  assert.equal(conflict.status,409)
  assert.equal((await owner.query("SELECT body,version FROM api.documents WHERE tenant='team-a' AND external_key='existing'")).rows[0].version,2)
  const fresh=approveImport(await previewImport(url,token,edit))
  const races=await Promise.all([commitImport(url,token,fresh),commitImport(url,token,fresh)])
  assert.deepEqual(races.map(r=>r.status).sort(),[200,409])
  // Delete/recreate of a key cannot reuse a prior approval even at version 1.
  const replaced=approveImport(await previewImport(url,token,[{key:'existing',body:'replace'}]))
  await owner.query("DELETE FROM api.documents WHERE tenant='team-a' AND external_key='existing'")
  assert.equal((await commitImport(url,token,replaced)).status,409)
  await owner.query("INSERT INTO api.documents(owner,tenant,external_key,body) VALUES('alice','team-a','existing','replacement')")
  assert.equal((await commitImport(url,token,replaced)).status,409)
  // An early successful insert rolls back if a later reviewed row conflicts.
  const mixed=approveImport(await previewImport(url,token,[{key:'a-rollback',body:'draft'},{key:'existing',body:'later'}]))
  await owner.query("UPDATE api.documents SET body='changed' WHERE tenant='team-a' AND external_key='existing'")
  assert.equal((await commitImport(url,token,mixed)).status,409)
  assert.equal((await owner.query("SELECT count(*)::integer AS count FROM api.documents WHERE external_key='a-rollback'")).rows[0].count,0)
  const hiddenApproval=approveImport(await previewImport(url,await sign('alice','team-b'),[{key:'hidden',body:'attempt'}]))
  assert.equal((await commitImport(url,token,hiddenApproval)).status,403)
  // Unguarded old signature is gone; missing expectations cannot overwrite data.
  assert.equal((await dataRequest(alice.rpc('commit_import',{items:edit}),{timeoutMs:5000})).status,404)
  assert.equal((await dataRequest(alice.rpc('commit_import',{items:edit,expected:[{}],review_identity:{subject:'alice',tenant:'team-a'}}),{timeoutMs:5000})).status,400)

  const otherNew=approveImport(await previewImport(url,await sign('alice','team-b'),[{key:'scope-new',body:'reviewed'}]))
  assert.equal((await commitImport(url,token,otherNew)).status,403)
  assert.equal((await owner.query("SELECT count(*)::integer AS count FROM api.documents WHERE external_key='scope-new'")).rows[0].count,0)

  const newRace=approveImport(await previewImport(url,token,[{key:'new-race',body:'draft'}]))
  const newResults=await Promise.all([commitImport(url,token,newRace),commitImport(url,token,newRace)])
  assert.deepEqual(newResults.map(r=>r.status).sort(),[200,409])

})
