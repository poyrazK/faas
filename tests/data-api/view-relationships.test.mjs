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

test('view relationships match typed PostgREST joins, aliases, cardinality, RLS and structural diffs', { skip: !enabled, timeout: 60000 }, async t => {
  const runtime = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/')
  const require = createRequire(new URL('package.json', runtime)), pg = require('pg')
  const { generateKeyPair, exportJWK, createLocalJWKSet, SignJWT } = await import(pathToFileURL(require.resolve('jose')))
  const { runtimeConfig } = await import(new URL('config.mjs', runtime))
  const { inspect, generate, fingerprint } = await import(new URL('types.mjs', runtime))
  const { createServer, tokenVerifier } = await import(new URL('server.mjs', runtime))
  const suffix = `${process.pid}_${Date.now()}`, database = `views_${suffix}`, role = `views_login_${suffix}`
  const admin = new pg.Client({ connectionString: process.env.DATA_API_TEST_DATABASE_URL })
  await admin.connect()
  let owner, child, gateway
  const directory = await mkdtemp(join(tmpdir(), 'gregale-view-joins-'))
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
    CREATE TABLE other.customers(id integer PRIMARY KEY); ALTER TABLE other.customers ENABLE ROW LEVEL SECURITY;
    CREATE TABLE api.customers(subject text NOT NULL,id integer NOT NULL,name text NOT NULL,PRIMARY KEY(subject,id));
    CREATE TABLE api.orders(subject text NOT NULL,id integer NOT NULL,buyer_id integer NOT NULL,gift_buyer_id integer,total integer NOT NULL,PRIMARY KEY(subject,id),
      CONSTRAINT orders_buyer_fkey FOREIGN KEY(subject,buyer_id) REFERENCES api.customers(subject,id),
      CONSTRAINT orders_gift_fkey FOREIGN KEY(subject,gift_buyer_id) REFERENCES api.customers(subject,id));
    CREATE TABLE api.invoices(subject text NOT NULL,order_id integer NOT NULL,amount integer NOT NULL,PRIMARY KEY(subject,order_id),
      CONSTRAINT invoices_order_fkey FOREIGN KEY(subject,order_id) REFERENCES api.orders(subject,id));
    ALTER TABLE api.customers ENABLE ROW LEVEL SECURITY; ALTER TABLE api.orders ENABLE ROW LEVEL SECURITY; ALTER TABLE api.invoices ENABLE ROW LEVEL SECURITY;
    CREATE POLICY own_customers ON api.customers USING(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND name<>'hidden');
    CREATE POLICY own_orders ON api.orders USING(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub');
    CREATE POLICY own_invoices ON api.invoices USING(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub');
    CREATE TABLE api.partition_orders(subject text NOT NULL,id integer,buyer_id integer,
      CONSTRAINT partition_buyer_fkey FOREIGN KEY(subject,buyer_id) REFERENCES api.customers(subject,id)) PARTITION BY LIST(subject);
    CREATE TABLE api.partition_alice PARTITION OF api.partition_orders FOR VALUES IN ('alice');
    ALTER TABLE api.partition_orders ENABLE ROW LEVEL SECURITY; ALTER TABLE api.partition_alice ENABLE ROW LEVEL SECURITY;
    CREATE POLICY own_partition ON api.partition_orders USING(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub');
    CREATE VIEW api.partition_cards WITH (security_invoker=true) AS SELECT subject,buyer_id FROM api.partition_orders;
    CREATE VIEW api.partition_leaf WITH (security_invoker=true) AS SELECT subject,buyer_id FROM api.partition_alice;
    CREATE VIEW api.order_cards WITH (security_invoker=true) AS SELECT id AS order_id,subject AS tenant,buyer_id AS customer_key,gift_buyer_id AS recipient_key,total FROM api.orders;
    CREATE VIEW api.customer_cards WITH (security_invoker=true) AS SELECT subject AS tenant,id AS customer_key,name AS title FROM api.customers;
    CREATE VIEW api.order_chain WITH (security_invoker=true) AS SELECT * FROM api.order_cards;
    CREATE VIEW api.invoice_cards WITH (security_invoker=true) AS SELECT subject AS tenant,order_id AS order_key,amount FROM api.invoices;
    CREATE VIEW api.partial_orders WITH (security_invoker=true) AS SELECT id,buyer_id FROM api.orders;
    CREATE VIEW api.computed_orders WITH (security_invoker=true) AS SELECT subject,buyer_id+0 AS buyer_id FROM api.orders;
    CREATE VIEW api.union_orders WITH (security_invoker=true) AS SELECT subject,buyer_id FROM api.orders UNION SELECT subject,buyer_id FROM api.orders;
    CREATE MATERIALIZED VIEW api.materialized_orders AS SELECT subject,buyer_id FROM api.orders;
    CREATE TABLE api.cross_orders(customer_id integer REFERENCES other.customers(id)); ALTER TABLE api.cross_orders ENABLE ROW LEVEL SECURITY;
    CREATE VIEW api.cross_cards WITH (security_invoker=true) AS SELECT customer_id FROM api.cross_orders;
    CREATE VIEW api.duplicate_orders WITH (security_invoker=true) AS SELECT id,subject,buyer_id AS first_key,buyer_id AS second_key FROM api.orders;
    CREATE VIEW api.hidden_orders WITH (security_invoker=true) AS SELECT subject,buyer_id FROM api.orders;
    CREATE TABLE api.private_rows(subject text,id integer,secret_column text,FOREIGN KEY(subject,id) REFERENCES api.customers(subject,id));
    CREATE VIEW api.private_projection WITH (security_invoker=true) AS SELECT subject,id FROM api.private_rows;
    CREATE VIEW api.strange_names WITH (security_invoker=true) AS SELECT subject AS "tenant :resorigtbl ({ })",buyer_id AS "customer_key\\name" FROM api.orders;
    GRANT USAGE ON SCHEMA api,other TO "${role}";
    GRANT SELECT ON api.customers,api.orders,api.invoices,api.order_cards,api.customer_cards,api.order_chain,api.invoice_cards,api.partial_orders,api.computed_orders,api.union_orders,api.materialized_orders,api.cross_orders,api.cross_cards,other.customers,api.duplicate_orders,api.private_projection,api.strange_names TO "${role}";
    GRANT SELECT(subject) ON api.hidden_orders TO "${role}";
    GRANT SELECT ON api.partition_orders,api.partition_alice,api.partition_cards,api.partition_leaf TO "${role}";
    INSERT INTO api.customers VALUES('alice',1,'Alice'),('alice',2,'Gift'),('bob',1,'Bob'),('alice',3,'hidden');
    INSERT INTO api.orders VALUES('alice',1,1,2,42),('bob',1,1,NULL,24),('alice',2,3,NULL,12);
    INSERT INTO api.partition_orders VALUES('alice',1,1);
    INSERT INTO api.invoices VALUES('alice',1,42),('bob',1,24)`)
  const loginURL = new URL(ownerURL); loginURL.username = role; loginURL.password = 'test-only'
  const snapshot = await inspect(loginURL.toString(), ['api'])
  const table = name => snapshot.tables.find(t => t.name === name)
  for (const name of ['partial_orders', 'computed_orders', 'union_orders', 'materialized_orders', 'cross_cards', 'hidden_orders', 'private_projection', 'partition_leaf']) assert.deepEqual(table(name).relationships, [], name)
  assert.equal(table('partition_cards').relationships.filter(r => r.foreignKeyName === 'partition_buyer_fkey').length, 2)
  const bothSchemas = await inspect(loginURL.toString(), ['api', 'other'])
  assert.deepEqual(bothSchemas.tables.find(t => t.schema === 'api' && t.name === 'cross_cards').relationships, [], 'a cross-schema FK cannot target the unrelated local customers table')
  assert.equal(table('private_rows'), undefined)
  const buyer = table('order_cards').relationships.find(r => r.foreignKeyName === 'orders_buyer_fkey' && r.referencedRelation === 'customer_cards')
  assert.deepEqual(buyer.columns, ['tenant', 'customer_key'])
  assert.deepEqual(buyer.referencedColumns, ['tenant', 'customer_key'])
  assert.equal(buyer.isOneToOne, false)
  assert.ok(table('order_chain').relationships.some(r => r.foreignKeyName === 'orders_buyer_fkey'))
  assert.ok(table('invoice_cards').relationships.filter(r => r.foreignKeyName === 'invoices_order_fkey').every(r => r.isOneToOne))
  assert.deepEqual(table('strange_names').relationships.find(r => r.referencedRelation === 'customers').columns, ['tenant :resorigtbl ({ })', 'customer_key\\name'])
  const types = generate(snapshot)
  assert.doesNotMatch(types, /secret_column|private_rows|test-only|views_login|resorigcol|pg_rewrite/)
  assert.equal(types, generate(await inspect(loginURL.toString(), ['api'])), 'stable catalog reads must produce identical contracts')
  const config = runtimeConfig({ DATABASE_URL: loginURL.toString(), DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'orders' })
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
import type {Database,Tables,TablesUpdate} from './database.types.js';
export async function exercise(url:string, token:string) {
 const db=createDataClient<Database>({url,accessToken:token}).schema('api');
 const card=await db.from('order_cards').select('order_id,buyer:customer_cards!orders_buyer_fkey(title),gift:customers!orders_gift_fkey(name),invoice:invoice_cards!invoices_order_fkey(amount)').eq('order_id',1).single();
 const chain=await db.from('order_chain').select('order_id,buyer:customers!orders_buyer_fkey(name)').eq('order_id',1).single();
 const parent=await db.from('customer_cards').select('customer_key,orders:order_cards!orders_buyer_fkey(order_id)').eq('customer_key',1).single();
 const inner=await db.from('order_cards').select('order_id,buyer:customer_cards!orders_buyer_fkey!inner(title)');
 const optional=await db.from('order_cards').select('order_id,buyer:customer_cards!orders_buyer_fkey(title)').eq('order_id',2).single();
 const partition=await db.from('partition_cards').select('buyer:customer_cards!partition_buyer_fkey(title)');
 const one: {amount:number|null}|null|undefined=card.data?.invoice;
 const buyer: {title:string|null}|null|undefined=card.data?.buyer;
 const many: {order_id:number|null}[]|undefined=parent.data?.orders;
 // @ts-expect-error Reverse one-to-many view joins return arrays.
 const wrongArray: {order_id:number|null}|undefined=parent.data?.orders;
 // @ts-expect-error One-to-one view joins return an object or null.
 const wrongOne: {amount:number|null}[]|undefined=card.data?.invoice;
 // @ts-expect-error Joined projections cannot invent unselected fields.
 const invented=card.data?.buyer?.customer_key;
 // @ts-expect-error View write helpers remain unavailable.
 type WriteView=TablesUpdate<'order_cards'>;
 const row: Tables<'order_cards'>={order_id:null,tenant:null,customer_key:null,recipient_key:null,total:null};
 void [one,buyer,many,wrongArray,wrongOne,invented,row];
 return {card,chain,parent,inner,optional,partition};
}`)
  const tsc = fileURLToPath(new URL('../../sdk/data/node_modules/typescript/bin/tsc', import.meta.url))
  execFileSync(process.execPath, [tsc, '--strict', '--skipLibCheck', '--target', 'ES2022', '--module', 'NodeNext', '--outDir', join(directory, 'dist'), join(directory, 'client.ts')], { encoding: 'utf8' })
  const client = await import(pathToFileURL(join(directory, 'dist/client.js')))
  const alice = await sign('alice'), bob = await sign('bob')
  const result = await client.exercise(url, alice)
  for (const response of Object.values(result)) assert.equal(response.error, null, JSON.stringify(response.error))
  assert.deepEqual(result.card.data, { order_id: 1, buyer: { title: 'Alice' }, gift: { name: 'Gift' }, invoice: { amount: 42 } })
  assert.deepEqual(result.chain.data, { order_id: 1, buyer: { name: 'Alice' } })
  assert.deepEqual(result.parent.data, { customer_key: 1, orders: [{ order_id: 1 }] })
  assert.deepEqual(result.inner.data, [{ order_id: 1, buyer: { title: 'Alice' } }])
  assert.deepEqual(result.optional.data, { order_id: 2, buyer: null })
  assert.deepEqual(result.partition.data, [{ buyer: { title: 'Alice' } }])
  const foreign = await client.exercise(url, bob)
  assert.deepEqual(foreign.card.data, { order_id: 1, buyer: { title: 'Bob' }, gift: null, invoice: { amount: 24 } })
  assert.deepEqual(foreign.parent.data, { customer_key: 1, orders: [{ order_id: 1 }] })
  const request = async (path, token = alice) => fetch(url + '/rest/v1/' + path, { headers: { Authorization: `Bearer ${token}` } })
  for (const view of ['order_cards', 'duplicate_orders']) {
    const response = await request(view + '?select=customers(name)')
    assert.equal(response.status, 300)
    assert.equal((await response.json()).code, 'PGRST201')
  }
  assert.equal((await (await request('duplicate_orders?select=customers!orders_buyer_fkey(name)')).json()).code, 'PGRST201', 'a constraint hint cannot select between repeated composite-key aliases')
  for (const view of ['partial_orders', 'computed_orders', 'union_orders', 'partition_leaf']) {
    const response = await request(view + '?select=customers(name)')
    assert.equal((await response.json()).code, 'PGRST200', view)
  }
  assert.equal((await request('private_projection')).status, 403)
  // Preserve the exported column shape, but remove the FK origin of one key.
  await owner.query('CREATE OR REPLACE VIEW api.order_cards WITH (security_invoker=true) AS SELECT id AS order_id,subject AS tenant,buyer_id+0 AS customer_key,gift_buyer_id AS recipient_key,total FROM api.orders')
  const next = await inspect(loginURL.toString(), ['api'])
  assert.deepEqual(next.tables.find(t => t.name === 'order_cards').columns, table('order_cards').columns)
  assert.ok(!next.tables.find(t => t.name === 'order_cards').relationships.some(r => r.foreignKeyName === 'orders_buyer_fkey'))
  assert.notEqual(fingerprint(snapshot), fingerprint(next))
  if (process.env.DATA_API_DEV_CLI) {
    for (const [name, value] of [['before', snapshot], ['after', next]]) await writeFile(join(directory, name + '.json'), JSON.stringify({ version: 1, fingerprint: fingerprint(value), snapshot: value }))
    const diff = spawn(process.env.DATA_API_DEV_CLI, ['--json', 'data-api', 'diff', '--baseline', join(directory, 'before.json'), '--current', join(directory, 'after.json'), '--check'], { stdio: ['ignore', 'pipe', 'pipe'] })
    let output = ''; diff.stdout.on('data', b => { output += b }); diff.stderr.resume()
    const [code] = await once(diff, 'exit')
    assert.equal(code, 1, output)
    const changes = JSON.parse(output).changes
    assert.ok(changes.some(c => c.path === 'api.order_cards[orders_buyer_fkey->customers]' && c.breaking && c.reason.startsWith('relationship removed')))
    assert.ok(!changes.some(c => c.reason.includes('field type changed')))
  }
  await owner.query(`CREATE VIEW api.oversized_definition AS SELECT '${'x'.repeat(300000)}'::text AS body`)
  await owner.query(`GRANT SELECT ON api.oversized_definition TO "${role}"`)
  await assert.rejects(inspect(loginURL.toString(), ['api']), /View lineage exceeds generation limits/)
})
