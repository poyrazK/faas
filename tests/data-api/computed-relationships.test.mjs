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

async function port() {
  const server = net.createServer()
  await new Promise(r => server.listen(0, '127.0.0.1', r))
  const value = server.address().port
  await new Promise(r => server.close(r))
  return value
}

test('computed relationships and RPC projections match typed PostgREST, RLS, grants and diffs', { skip: !enabled, timeout: 60000 }, async t => {
  const runtime = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/')
  const require = createRequire(new URL('package.json', runtime)), pg = require('pg')
  const { generateKeyPair, exportJWK, createLocalJWKSet, SignJWT } = await import(pathToFileURL(require.resolve('jose')))
  const { runtimeConfig } = await import(new URL('config.mjs', runtime))
  const { inspect, generate, fingerprint, computedPermissionBlockers } = await import(new URL('types.mjs', runtime))
  const { createServer, tokenVerifier } = await import(new URL('server.mjs', runtime))
  const { inspector } = await import(new URL('dev-inspector.mjs', runtime))
  const suffix = `${process.pid}_${Date.now()}`, database = `computed_${suffix}`, role = `computed_login_${suffix}`
  const admin = new pg.Client({ connectionString: process.env.DATA_API_TEST_DATABASE_URL })
  await admin.connect()
  let owner, child, gateway
  const directory = await mkdtemp(join(tmpdir(), 'gregale-computed-joins-'))
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
  await owner.query(`REVOKE ALL ON SCHEMA public FROM PUBLIC; CREATE SCHEMA api; CREATE SCHEMA other;
    CREATE TABLE api.customers(subject text NOT NULL,id integer NOT NULL,name text NOT NULL,PRIMARY KEY(subject,id));
    CREATE TABLE api.orders(subject text NOT NULL,id integer NOT NULL,customer_id integer NOT NULL,PRIMARY KEY(subject,id),
      FOREIGN KEY(subject,customer_id) REFERENCES api.customers(subject,id));
    CREATE TABLE api.invoices(subject text NOT NULL,id integer NOT NULL,order_id integer NOT NULL,total integer NOT NULL,PRIMARY KEY(subject,id),
      FOREIGN KEY(subject,order_id) REFERENCES api.orders(subject,id));
    ALTER TABLE api.customers ENABLE ROW LEVEL SECURITY; ALTER TABLE api.orders ENABLE ROW LEVEL SECURITY; ALTER TABLE api.invoices ENABLE ROW LEVEL SECURITY;
    CREATE POLICY own_customers ON api.customers USING(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub');
    CREATE POLICY own_orders ON api.orders USING(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub');
    CREATE POLICY own_invoices ON api.invoices USING(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND id<>103);
    CREATE VIEW api.order_cards WITH (security_invoker=true) AS SELECT subject AS tenant,id AS order_key FROM api.orders;
    CREATE VIEW api.invoice_cards WITH (security_invoker=true) AS SELECT subject,id,total FROM api.invoices;
    CREATE TABLE other.invoices(subject text,id integer,total integer);
    CREATE FUNCTION api.latest_invoice(source_row api.orders) RETURNS SETOF api.invoices ROWS 1 LANGUAGE sql STABLE SECURITY INVOKER SET search_path=pg_catalog
      AS 'SELECT i.* FROM api.invoices i WHERE i.subject=source_row.subject AND i.order_id=source_row.id ORDER BY i.id DESC LIMIT 1';
    CREATE FUNCTION api.invoice_history(source_row api.orders) RETURNS SETOF api.invoices LANGUAGE sql STABLE SECURITY INVOKER SET search_path=pg_catalog
      AS 'SELECT i.* FROM api.invoices i WHERE i.subject=source_row.subject AND i.order_id=source_row.id ORDER BY i.id';
    CREATE FUNCTION api.latest_card_invoice(source_row api.order_cards) RETURNS SETOF api.invoices ROWS 1 LANGUAGE sql STABLE SECURITY INVOKER SET search_path=pg_catalog
      AS 'SELECT i.* FROM api.invoices i WHERE i.subject=source_row.tenant AND i.order_id=source_row.order_key ORDER BY i.id DESC LIMIT 1';
    CREATE FUNCTION api.invoice_summary(source_row api.orders) RETURNS SETOF api.invoice_cards ROWS 1 LANGUAGE sql STABLE SECURITY INVOKER SET search_path=pg_catalog
      AS 'SELECT i.subject,i.id,i.total FROM api.invoices i WHERE i.subject=source_row.subject AND i.order_id=source_row.id ORDER BY i.id DESC LIMIT 1';
    CREATE FUNCTION api.related_orders(source_row api.orders) RETURNS SETOF api.orders LANGUAGE sql STABLE SECURITY INVOKER SET search_path=pg_catalog
      AS 'SELECT o.* FROM api.orders o WHERE o.subject=source_row.subject AND o.id<>source_row.id ORDER BY o.id';
    CREATE FUNCTION api.search_invoices(min_total integer DEFAULT 0) RETURNS SETOF api.invoices LANGUAGE sql STABLE SECURITY INVOKER SET search_path=pg_catalog
      AS 'SELECT i.* FROM api.invoices i WHERE i.total>=min_total ORDER BY i.id';
    CREATE FUNCTION api.sneaky(source_row api.orders) RETURNS SETOF api.invoices ROWS 1 LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog
      AS 'SELECT i.* FROM api.invoices i WHERE i.subject=source_row.subject LIMIT 1';
    CREATE FUNCTION api.cross_target(source_row api.orders) RETURNS SETOF other.invoices LANGUAGE sql STABLE SECURITY INVOKER AS 'SELECT * FROM other.invoices';
    CREATE FUNCTION api.overloaded(source_row api.orders) RETURNS SETOF api.invoices LANGUAGE sql STABLE AS 'SELECT * FROM api.invoices';
    CREATE FUNCTION api.overloaded(source_row api.customers) RETURNS SETOF api.invoices LANGUAGE sql STABLE AS 'SELECT * FROM api.invoices';
    CREATE FUNCTION api.volatile_join(source_row api.orders) RETURNS SETOF api.invoices LANGUAGE sql VOLATILE AS 'SELECT * FROM api.invoices';
    REVOKE ALL ON ALL FUNCTIONS IN SCHEMA api FROM PUBLIC;
    COMMENT ON FUNCTION api.latest_invoice(api.orders) IS '@gregale:rpc';
    COMMENT ON FUNCTION api.invoice_history(api.orders) IS '@gregale:rpc';
    COMMENT ON FUNCTION api.latest_card_invoice(api.order_cards) IS '@gregale:rpc';
    COMMENT ON FUNCTION api.invoice_summary(api.orders) IS '@gregale:rpc';
    COMMENT ON FUNCTION api.related_orders(api.orders) IS '@gregale:rpc';
    COMMENT ON FUNCTION api.search_invoices(integer) IS '@gregale:rpc';
    COMMENT ON FUNCTION api.cross_target(api.orders) IS '@gregale:rpc';
    COMMENT ON FUNCTION api.overloaded(api.orders) IS '@gregale:rpc';
    COMMENT ON FUNCTION api.volatile_join(api.orders) IS '@gregale:rpc';
    GRANT USAGE ON SCHEMA api TO "${role}";
    GRANT SELECT ON ALL TABLES IN SCHEMA api TO "${role}";
    GRANT EXECUTE ON FUNCTION api.latest_invoice(api.orders),api.invoice_history(api.orders),api.latest_card_invoice(api.order_cards),api.invoice_summary(api.orders),api.related_orders(api.orders),api.search_invoices(integer) TO "${role}";
    INSERT INTO api.customers VALUES('alice',1,'Alice'),('bob',1,'Bob');
    INSERT INTO api.orders VALUES('alice',1,1),('alice',2,1),('bob',1,1);
    INSERT INTO api.invoices VALUES('alice',101,1,42),('alice',102,1,84),('alice',103,1,999),('bob',101,1,24)`)
  const loginURL = new URL(ownerURL); loginURL.username = role; loginURL.password = 'test-only'
  const snapshot = await inspect(loginURL.toString(), ['api'])
  assert.equal(snapshot.version, 4)
  assert.deepEqual(snapshot.functions.map(f => f.name), ['invoice_history', 'invoice_summary', 'latest_card_invoice', 'latest_invoice', 'related_orders', 'search_invoices'])
  assert.deepEqual(snapshot.functions.find(f => f.name === 'latest_invoice').setof, { from: 'orders', to: 'invoices', one_to_one: true })
  assert.deepEqual(snapshot.functions.find(f => f.name === 'search_invoices').setof, { from: '', to: 'invoices', one_to_one: false })
  const types = generate(snapshot)
  assert.equal(types, generate(await inspect(loginURL.toString(), ['api'])))
  assert.doesNotMatch(types, /sneaky|cross_target|overloaded|volatile_join|test-only|pg_proc|prorows/)
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
  await writeFile(join(directory, 'database.types.ts'), types)
  const sdk = fileURLToPath(new URL('../../sdk/data/dist/index.js', import.meta.url))
  await writeFile(join(directory, 'client.ts'), `import {createDataClient} from ${JSON.stringify(sdk)};
import type {Database} from './database.types.js';
export async function exercise(url:string, token:string, subject:string) {
 const db=createDataClient<Database>({url,accessToken:token}).schema('api');
 const order=await db.from('orders').select('id,latest:latest_invoice(id,total,parent:orders(id,customer:customers(name))),history:invoice_history(id),summary:invoice_summary(total),related:related_orders(id)').eq('id',1).single();
 const empty=await db.from('orders').select('id,latest_invoice(total),invoice_history(id)').eq('id',2).single();
 const view=await db.from('order_cards').select('order_key,latest_card_invoice(total)').eq('order_key',1).single();
 const inner=await db.from('orders').select('id,latest_invoice!inner(total)');
 const rpc=await db.rpc('search_invoices',{min_total:0}).select('id,total,parent:orders(id,customer:customers(name))').gte('total',20).order('id');
 const direct=await db.rpc('latest_invoice',{source_row:{subject,id:1,customer_id:1}}).select('total,parent:orders(id)');
 const singular: {id:number,total:number,parent:{id:number,customer:{name:string}}}|null|undefined=order.data?.latest;
 const many: {id:number}[]|undefined=order.data?.history;
 const viewTarget: {total:number|null}|null|undefined=order.data?.summary;
 const directArray: {total:number,parent:{id:number}}[]|null=direct.data;
 // @ts-expect-error A ROWS 1 computed relationship returns an object or null.
 const wrongOne: {id:number}[]|undefined=order.data?.latest;
 // @ts-expect-error Multi-row computed relationships return arrays.
 const wrongMany: {id:number}|undefined=order.data?.history;
 // @ts-expect-error Joined projections cannot invent unselected fields.
 const invented=rpc.data?.[0].parent.customer.subject;
 // @ts-expect-error Direct ROWS 1 RPC calls still return arrays.
 const wrongRPC: {total:number,parent:{id:number}}|null=direct.data;
 void [singular,many,viewTarget,directArray,wrongOne,wrongMany,invented,wrongRPC];
 return {order,empty,view,inner,rpc,direct};
}`)
  const tsc = fileURLToPath(new URL('../../sdk/data/node_modules/typescript/bin/tsc', import.meta.url))
  execFileSync(process.execPath, [tsc, '--strict', '--skipLibCheck', '--target', 'ES2022', '--module', 'NodeNext', '--outDir', join(directory, 'dist'), join(directory, 'client.ts')], { encoding: 'utf8' })
  const client = await import(pathToFileURL(join(directory, 'dist/client.js')))
  const alice = await sign('alice'), bob = await sign('bob')
  const result = await client.exercise(url, alice, 'alice')
  for (const [name, response] of Object.entries(result)) if (name !== 'empty') assert.equal(response.error, null, JSON.stringify(response.error))
  assert.deepEqual(result.order.data, { id: 1, latest: { id: 102, total: 84, parent: { id: 1, customer: { name: 'Alice' } } }, history: [{ id: 101 }, { id: 102 }], summary: { total: 84 }, related: [{ id: 2 }] })
  assert.deepEqual(result.empty.data, { id: 2, latest_invoice: null, invoice_history: [] })
  assert.deepEqual(result.view.data, { order_key: 1, latest_card_invoice: { total: 84 } })
  assert.deepEqual(result.inner.data, [{ id: 1, latest_invoice: { total: 84 } }])
  assert.deepEqual(result.rpc.data, [{ id: 101, total: 42, parent: { id: 1, customer: { name: 'Alice' } } }, { id: 102, total: 84, parent: { id: 1, customer: { name: 'Alice' } } }])
  assert.deepEqual(result.direct.data, [{ total: 84, parent: { id: 1 } }])
  const foreign = await client.exercise(url, bob, 'bob')
  assert.deepEqual(foreign.order.data.latest, { id: 101, total: 24, parent: { id: 1, customer: { name: 'Bob' } } })
  assert.deepEqual(foreign.order.data.history, [{ id: 101 }])
  assert.deepEqual(foreign.order.data.related, [])
  assert.deepEqual(foreign.rpc.data, [{ id: 101, total: 24, parent: { id: 1, customer: { name: 'Bob' } } }])
  const request = async (path, init = {}) => fetch(url + path, { ...init, headers: { Authorization: `Bearer ${alice}`, 'Content-Type': 'application/json' } })
  const forged = await request('/rest/v1/rpc/latest_invoice', { method: 'POST', body: JSON.stringify({ source_row: { subject: 'bob', id: 1, customer_id: 1 } }) })
  assert.deepEqual(await forged.json(), [])
  assert.equal((await request('/rest/v1/rpc/latest_invoice')).status, 405)
  assert.equal((await request('/rest/v1/rpc/sneaky', { method: 'POST', body: '{}' })).status, 404)
  assert.equal((await request('/rest/v1/orders?select=id,sneaky(id)')).status, 403, 'SQL execution grants protect nested relationships')
  const spec = await (await request('/openapi.json')).json()
  assert.equal(spec.paths['/rpc/sneaky'], undefined)
  assert.equal(spec.paths['/rpc/latest_invoice'].get, undefined)
  assert.deepEqual(spec.paths['/rpc/latest_invoice'].post.responses[200].schema, { type: 'array', items: { $ref: '#/definitions/api.invoices.Row' } })
  assert.ok(spec.paths['/orders'].get['x-gregale-computed-relationships'].some(r => r.name === 'latest_invoice' && r.one_to_one))
  if (process.env.DATA_API_CHROMIUM_BIN) {
    localInspector = await inspector(() => ({ url, ready: true, status: 'ready', client_checked: true, fingerprint: fingerprint(snapshot), snapshot,
      identities: { alice: { subject: 'alice', token: alice }, bob: { subject: 'bob', token: bob } } }), join(directory, 'requests.json'), ['alice', 'bob'])
    const { browserInspector } = await import('./browser-inspector.mjs')
    await browserInspector(localInspector.url(url), async evaluate => {
      const wait = async expression => {
        for (let i = 0; i < 200; i++) { if (await evaluate(expression)) return; await new Promise(r => setTimeout(r, 50)) }
        assert.fail('Computed relationship inspector did not reach expected state')
      }
      await wait("document.getElementById('send')?.disabled === false")
      await evaluate("{const resources=document.getElementById('resource'); resources.value=[...resources.options].find(option=>option.textContent==='Table · api.orders').value; resources.dispatchEvent(new Event('change'))}")
      assert.ok(await evaluate("document.getElementById('schema').textContent.includes('computedRelationships') && document.getElementById('schema').textContent.includes('latest_invoice')"))
      await evaluate("{const resources=document.getElementById('resource'); resources.value=[...resources.options].find(option=>option.textContent==='RPC + relationship · api.latest_invoice').value; resources.dispatchEvent(new Event('change')); document.getElementById('path').value+='?select=total'; document.getElementById('body').value=JSON.stringify({source_row:{subject:'alice',id:1,customer_id:1}}); document.getElementById('send').click()}")
      await wait("!document.getElementById('send').disabled && document.getElementById('result-status').textContent.startsWith('200')")
      assert.deepEqual(JSON.parse(await evaluate("document.getElementById('response').textContent")), [{ total: 84 }])
      await evaluate("document.getElementById('identity').value='bob'; document.getElementById('send').click()")
      await wait("!document.getElementById('send').disabled && document.getElementById('response').textContent === '[]'")
    })
  } else assert.notEqual(process.env.DATA_API_BROWSER_REQUIRED, '1', 'Chromium required for computed relationship inspector acceptance')
  // PUBLIC and explicit binding grants both make an unapproved embed callable.
  for (const recipient of ['PUBLIC', `"${role}"`]) {
    await owner.query(`GRANT EXECUTE ON FUNCTION api.sneaky(api.orders) TO ${recipient}`)
    assert.ok((await computedPermissionBlockers(owner, ['api'], role, snapshot)).includes('computed_relationship_not_approved:api.sneaky'))
    await assert.rejects(inspect(loginURL.toString(), ['api']), /Unapproved computed relationship execution/)
    await owner.query(`REVOKE EXECUTE ON FUNCTION api.sneaky(api.orders) FROM ${recipient}`)
    assert.equal(generate(await inspect(loginURL.toString(), ['api'])), types)
  }
  await owner.query(`GRANT EXECUTE ON FUNCTION api.overloaded(api.orders) TO "${role}"`)
  await assert.rejects(inspect(loginURL.toString(), ['api']), /Unapproved computed relationship execution/)
  await owner.query(`REVOKE EXECUTE ON FUNCTION api.overloaded(api.orders) FROM "${role}"`)
  await owner.query('ALTER FUNCTION api.latest_invoice(api.orders) ROWS 1000')
  const next = await inspect(loginURL.toString(), ['api'])
  assert.equal(next.functions.find(f => f.name === 'latest_invoice').returns, snapshot.functions.find(f => f.name === 'latest_invoice').returns)
  assert.equal(next.functions.find(f => f.name === 'latest_invoice').setof.one_to_one, false)
  assert.notEqual(fingerprint(snapshot), fingerprint(next))
  if (process.env.DATA_API_DEV_CLI) {
    for (const [name, value] of [['before', snapshot], ['after', next]]) await writeFile(join(directory, name + '.json'), JSON.stringify({ version: 1, fingerprint: fingerprint(value), snapshot: value }))
    const diff = spawn(process.env.DATA_API_DEV_CLI, ['--json', 'data-api', 'diff', '--baseline', join(directory, 'before.json'), '--current', join(directory, 'after.json'), '--check'], { stdio: ['ignore', 'pipe', 'pipe'] })
    let output = ''; diff.stdout.on('data', b => { output += b }); diff.stderr.resume()
    const [code] = await once(diff, 'exit')
    assert.equal(code, 1, output)
    const changes = JSON.parse(output).changes
    assert.ok(changes.some(c => c.path === 'api.latest_invoice' && c.breaking && c.reason.includes('result relationship metadata')))
    assert.ok(!changes.some(c => c.reason.includes('return type changed')))
  }
})
