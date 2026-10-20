import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import http from 'node:http'
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

test('typed JSON projections match JSON/JSONB paths, null semantics, RPCs, writes, grants and RLS', { skip: !enabled, timeout: 60000 }, async t => {
  const runtime = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/')
  const require = createRequire(new URL('package.json', runtime)), pg = require('pg')
  const { generateKeyPair, exportJWK, createLocalJWKSet, SignJWT } = await import(pathToFileURL(require.resolve('jose')))
  const { runtimeConfig } = await import(new URL('config.mjs', runtime))
  const { inspect, generate, fingerprint } = await import(new URL('types.mjs', runtime))
  const { createServer, tokenVerifier } = await import(new URL('server.mjs', runtime))
  const { inspector } = await import(new URL('dev-inspector.mjs', runtime))
  const suffix = `${process.pid}_${Date.now()}`, database = `json_${suffix}`, role = `json_login_${suffix}`
  const admin = new pg.Client({ connectionString: process.env.DATA_API_TEST_DATABASE_URL })
  await admin.connect()
  let owner, child, gateway
  const directory = await mkdtemp(join(tmpdir(), 'gregale-json-'))
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
  await owner.query(`REVOKE ALL ON SCHEMA public FROM PUBLIC; CREATE SCHEMA api;
    CREATE TABLE api.orders(subject text NOT NULL,id integer NOT NULL,metadata jsonb,legacy json,strict_json jsonb NOT NULL DEFAULT '{}',PRIMARY KEY(subject,id));
    ALTER TABLE api.orders ENABLE ROW LEVEL SECURITY;
    CREATE POLICY own_orders ON api.orders USING(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub') WITH CHECK(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub');
    CREATE VIEW api.order_cards WITH (security_invoker=true) AS SELECT subject,id,metadata FROM api.orders;
    CREATE TABLE api.private_docs(id integer,secret jsonb); INSERT INTO api.private_docs VALUES(1,'{"secret":"hidden"}');
    CREATE FUNCTION api.json_orders() RETURNS SETOF api.orders LANGUAGE sql STABLE SECURITY INVOKER SET search_path=pg_catalog AS 'SELECT * FROM api.orders';
    REVOKE ALL ON FUNCTION api.json_orders() FROM PUBLIC; COMMENT ON FUNCTION api.json_orders() IS '@gregale:rpc';
    GRANT USAGE ON SCHEMA api TO "${role}";
    GRANT SELECT ON api.orders,api.order_cards TO "${role}"; GRANT SELECT(id) ON api.private_docs TO "${role}";
    GRANT UPDATE(metadata) ON api.orders TO "${role}";
    GRANT EXECUTE ON FUNCTION api.json_orders() TO "${role}"`)
  const metadata = JSON.stringify({ status: 'paid', delivery: { city: 'Paris' }, items: [{ name: 'first' }, { name: 'last' }], details: { priority: 2 }, null_value: null, string_null: 'null', 0: 'zero', 'comma,key': { status: 'quoted' }, number: 42, boolean: false })
  for (const [subject, id, value] of [['alice', 1, metadata], ['alice', 2, '{}'], ['alice', 3, null], ['alice', 4, 'null'], ['bob', 1, '{"status":"paid","delivery":{"city":"Rome"}}']]) {
    await owner.query('INSERT INTO api.orders(subject,id,metadata,legacy) VALUES($1,$2,$3::jsonb,$3::json)', [subject, id, value])
  }
  const loginURL = new URL(ownerURL); loginURL.username = role; loginURL.password = 'test-only'
  const snapshot = await inspect(loginURL.toString(), ['api'])
  assert.equal(snapshot.tables.find(t => t.name === 'orders').columns.find(c => c.name === 'strict_json').nullable, false)
  assert.deepEqual(snapshot.tables.find(t => t.name === 'private_docs').columns.map(c => c.name), ['id'])
  const config = runtimeConfig({ DATABASE_URL: loginURL.toString(), DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'orders' })
  config.snapshot = snapshot; config.functions = snapshot.functions; config.fingerprint = fingerprint(snapshot)
  const upstream = await port(), ready = await port()
  child = spawn(process.env.DATA_API_POSTGREST_BIN, [], { env: { ...process.env, ...config.postgrestEnv, PGRST_SERVER_PORT: String(upstream), PGRST_ADMIN_SERVER_PORT: String(ready) }, stdio: ['ignore', 'pipe', 'pipe'] })
  let logs = ''; child.stdout.on('data', b => { logs += b }); child.stderr.on('data', b => { logs += b })
  const { privateKey, publicKey } = await generateKeyPair('ES256'), jwk = await exportJWK(publicKey)
  const sign = sub => new SignJWT({ sub, role: 'postgres' }).setProtectedHeader({ alg: 'ES256' }).setIssuer(config.auth.issuer).setAudience(config.auth.audience).setExpirationTime('5m').sign(privateKey)
  let localInspector
  const api = createServer(config, tokenVerifier(config.auth, createLocalJWKSet({ keys: [jwk] })), upstream, ready, () => {})
  gateway = http.createServer((req, res) => { if (!localInspector?.handle(req, res)) api.emit('request', req, res) })
  await new Promise(r => gateway.listen(0, '127.0.0.1', r))
  const url = `http://127.0.0.1:${gateway.address().port}`
  for (let i = 0; i < 100 && (await fetch(url + '/healthz')).status !== 200 && child.exitCode === null; i++) await new Promise(r => setTimeout(r, 50))
  assert.equal((await fetch(url + '/healthz')).status, 200, logs)
  await writeFile(join(directory, 'package.json'), '{"type":"module"}')
  await writeFile(join(directory, 'database.types.ts'), generate(snapshot))
  const sdk = fileURLToPath(new URL('../../sdk/data/dist/index.js', import.meta.url))
  await writeFile(join(directory, 'client.ts'), `import {createDataClient,selectJson,jsonText,jsonValue,type Json} from ${JSON.stringify(sdk)};
import type {Database} from './database.types.js';
export async function exercise(url:string, token:string) {
 const db=createDataClient<Database>({url,accessToken:token}).schema('api');
 const fields={status:jsonText('metadata','status'),city:jsonText('metadata','delivery','city'),first:jsonText('metadata','items',0,'name'),last:jsonText('metadata','items',-1,'name'),details:jsonValue('metadata','details'),nil:jsonValue('metadata','null_value'),textNil:jsonText('metadata','null_value'),stringNull:jsonText('metadata','string_null'),zeroKey:jsonText('metadata','0'),quoted:jsonText('metadata','comma,key','status'),num:jsonText('metadata','number'),bool:jsonText('metadata','boolean'),legacy:jsonText('legacy','delivery','city'),legacyDetails:jsonValue('legacy','details'),missing:jsonText('strict_json','missing')};
 const rows=await selectJson(db.from('orders'),'id',fields).order('id');
 const filtered=await selectJson(db.from('orders'),'id',{status:jsonText('metadata','status')}).eq('metadata->>status','paid');
 const view=await selectJson(db.from('order_cards'),'id',{city:jsonText('metadata','delivery','city')}).eq('id',1).single();
 const rpc=await selectJson(db.rpc('json_orders'),'id',{city:jsonText('metadata','delivery','city')}).eq('id',1).single();
 const sqlNull=await db.from('orders').select('id').is('metadata',null);
 const jsonNull=await db.from('orders').select('id').eq('metadata','null');
 const pathNull=await db.from('orders').select('id').is('metadata->>status',null).order('id');
 const forbidden=await db.from('private_docs').select('secret->>secret').retry(false);
 const city:string|null|undefined=rows.data?.[0].city;
 const value:Json|undefined=rows.data?.[0].details;
 // @ts-expect-error JSON paths can be missing even in NOT NULL JSON columns.
 const required:string|undefined=rows.data?.[0].missing;
 // @ts-expect-error Text extraction cannot promise a numeric value.
 const numeric:number|null|undefined=rows.data?.[0].num;
 // @ts-expect-error A hidden source column is not in the generated contract.
 selectJson(db.from('private_docs'),'id',{hidden:jsonText('secret','secret')});
 // @ts-expect-error Arbitrary JSON does not declare a priority field.
 const key=rows.data?.[0].details?.priority;
 void [city,value,required,numeric,key];
 return {rows,filtered,view,rpc,sqlNull,jsonNull,pathNull,forbidden};
}
export async function write(url:string,token:string) {
 const db=createDataClient<Database>({url,accessToken:token}).schema('api');
 return selectJson(db.from('orders').update({metadata:{status:'changed'}}),'id',{status:jsonText('metadata','status')}).eq('id',2).single();
}`)
  const tsc = fileURLToPath(new URL('../../sdk/data/node_modules/typescript/bin/tsc', import.meta.url))
  execFileSync(process.execPath, [tsc, '--strict', '--skipLibCheck', '--target', 'ES2022', '--module', 'NodeNext', '--outDir', join(directory, 'dist'), join(directory, 'client.ts')], { encoding: 'utf8' })
  const client = await import(pathToFileURL(join(directory, 'dist/client.js')))
  const alice = await sign('alice'), bob = await sign('bob')
  const result = await client.exercise(url, alice)
  for (const [name, response] of Object.entries(result)) if (name !== 'forbidden') assert.equal(response.error, null, `${name}: ${JSON.stringify(response.error)}`)
  assert.deepEqual(result.rows.data[0], { id: 1, status: 'paid', city: 'Paris', first: 'first', last: 'last', details: { priority: 2 }, nil: null, textNil: null, stringNull: 'null', zeroKey: 'zero', quoted: 'quoted', num: '42', bool: 'false', legacy: 'Paris', legacyDetails: { priority: 2 }, missing: null })
  for (const row of result.rows.data.slice(1)) assert.ok(Object.entries(row).every(([key, value]) => key === 'id' || value === null))
  assert.deepEqual(result.filtered.data, [{ id: 1, status: 'paid' }])
  assert.deepEqual(result.view.data, { id: 1, city: 'Paris' })
  assert.deepEqual(result.rpc.data, { id: 1, city: 'Paris' })
  assert.deepEqual(result.sqlNull.data, [{ id: 3 }])
  assert.deepEqual(result.jsonNull.data, [{ id: 4 }])
  assert.deepEqual(result.pathNull.data, [{ id: 2 }, { id: 3 }, { id: 4 }])
  assert.equal(result.forbidden.status, 403)
  const foreign = await client.exercise(url, bob)
  assert.equal(foreign.rows.data.length, 1)
  assert.equal(foreign.rows.data[0].city, 'Rome')
  assert.deepEqual(foreign.rpc.data, { id: 1, city: 'Rome' })
  if (process.env.DATA_API_CHROMIUM_BIN) {
    localInspector = await inspector(() => ({ url, ready: true, status: 'ready', client_checked: true, fingerprint: fingerprint(snapshot), snapshot,
      identities: { alice: { subject: 'alice', token: alice }, bob: { subject: 'bob', token: bob } } }), join(directory, 'requests.json'), ['alice', 'bob'])
    const { browserInspector } = await import('./browser-inspector.mjs')
    await browserInspector(localInspector.url(url), async evaluate => {
      const wait = async expression => {
        for (let i = 0; i < 200; i++) { if (await evaluate(expression)) return; await new Promise(r => setTimeout(r, 50)) }
        assert.fail('JSON inspector did not reach expected state')
      }
      await wait("document.getElementById('send')?.disabled === false")
      await evaluate("{const resources=document.getElementById('resource'); resources.value=[...resources.options].find(o=>o.textContent==='Table · api.orders').value; resources.dispatchEvent(new Event('change'))}")
      assert.ok(await evaluate("!document.getElementById('json-help').hidden && document.getElementById('json-example').textContent.includes('selectJson')"))
      await evaluate("document.getElementById('path').value='/rest/v1/orders?select=id,city:metadata->delivery->>city&id=eq.1'; document.getElementById('send').click()")
      await wait("!document.getElementById('send').disabled && document.getElementById('result-status').textContent.startsWith('200')")
      assert.deepEqual(JSON.parse(await evaluate("document.getElementById('response').textContent")), [{ id: 1, city: 'Paris' }])
      await evaluate("document.getElementById('identity').value='bob'; document.getElementById('send').click()")
      await wait("!document.getElementById('send').disabled && document.getElementById('response').textContent.includes('Rome')")
    })
  } else assert.notEqual(process.env.DATA_API_BROWSER_REQUIRED, '1', 'Chromium required for JSON inspector acceptance')
  const changed = await client.write(url, alice)
  assert.equal(changed.error, null, JSON.stringify(changed.error))
  assert.deepEqual(changed.data, { id: 2, status: 'changed' })
  assert.equal((await client.exercise(url, bob)).rows.data[0].city, 'Rome')
})
