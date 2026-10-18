import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import { spawn, execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { once } from 'node:events'
import { mkdtemp, mkdir, readFile, writeFile, symlink, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
import { createRequire } from 'node:module'
import { randomUUID } from 'node:crypto'
import { mutationJournalBrowserOrigin, verifyMutationJournalBrowser } from './mutation-journal-browser.mjs'
import { createDataClient, dataRequest, reconcileMutation } from '../../sdk/data/dist/index.js'

const execute = promisify(execFile)
const enabled = Boolean(process.env.DATA_API_TEST_DATABASE_URL && process.env.DATA_API_POSTGREST_BIN && process.env.DATA_API_RUNTIME_DIR && process.env.DATA_API_DEV_CLI)
async function port() {
  const server = net.createServer()
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const value = server.address().port; await new Promise(resolve => server.close(resolve)); return value
}
const keyAt = milliseconds => {
  const time = Math.floor(milliseconds).toString(16).padStart(12, '0')
  return `${time.slice(0, 8)}-${time.slice(8)}-7${randomUUID().slice(15)}`
}

test('v2 CLI transaction package recovers original atomic outcomes with protected expiry and owner cleanup', { skip: !enabled, timeout: 150000 }, async t => {
  const root = await mkdtemp(join(tmpdir(), 'gregale-transaction-v2-')); t.after(() => rm(root, { recursive: true, force: true }))
  const output = join(root, 'package')
  const generated = JSON.parse((await execute(process.env.DATA_API_DEV_CLI, ['--json', 'data-api', 'transaction', 'init', '--name', 'place_order', '--contract', 'v2', '--output', output])).stdout)
  assert.equal(generated.function, 'api.place_order_v2'); assert.equal(generated.receipt_function, 'api.read_place_order_receipt_v2'); assert.equal(generated.status, 'needs_review')
  const runtime = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/'), require = createRequire(new URL('package.json', runtime)), pg = require('pg')
  const { inspect, fingerprint } = await import(new URL('types.mjs', runtime))
  const { generateValidators } = await import(new URL('type-render.mjs', runtime))
  const { permissions } = await import(new URL('rpc-permissions.mjs', runtime))
  const { runtimeConfig } = await import(new URL('config.mjs', runtime)), { createServer, tokenVerifier } = await import(new URL('server.mjs', runtime))
  const { generateKeyPair, exportJWK, createLocalJWKSet, SignJWT } = await import(pathToFileURL(require.resolve('jose')))
  const suffix = `${process.pid}_${Date.now()}`, database = `transactions_v2_${suffix}`, role = `tx_v2_login_${suffix}`, schemaOwner = `tx_v2_owner_${suffix}`, migrator = `tx_v2_migration_${suffix}`
  const admin = new pg.Client({ connectionString: process.env.DATA_API_TEST_DATABASE_URL }); await admin.connect()
  let owner, child, gateway, direct, lock, browser
  t.after(async () => {
    await browser?.close(); await lock?.end(); await direct?.end()
    if (gateway) { gateway.closeAllConnections(); await new Promise(resolve => gateway.close(resolve)) }
    if (child && child.exitCode === null) { child.kill(); await once(child, 'exit') }
    await owner?.end(); await admin.query(`DROP DATABASE IF EXISTS "${database}" WITH (FORCE)`)
    for (const name of [migrator, role, schemaOwner]) await admin.query(`DROP ROLE IF EXISTS "${name}"`)
    await admin.end()
  })
  await admin.query(`CREATE ROLE "${schemaOwner}" NOLOGIN NOBYPASSRLS;
    CREATE ROLE "${migrator}" LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD 'test-only';
    GRANT "${schemaOwner}" TO "${migrator}"; ALTER ROLE "${migrator}" SET role TO "${schemaOwner}";
    CREATE ROLE "${role}" LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD 'test-only';
    ALTER ROLE "${role}" SET statement_timeout=15000`)
  await admin.query(`CREATE DATABASE "${database}" OWNER "${schemaOwner}"`)
  await admin.query(`REVOKE ALL ON DATABASE "${database}" FROM PUBLIC; GRANT CONNECT ON DATABASE "${database}" TO "${migrator}","${role}"; GRANT CONNECT,CREATE ON DATABASE "${database}" TO "${schemaOwner}"`)
  const dbURL = new URL(process.env.DATA_API_TEST_DATABASE_URL); dbURL.pathname = `/${database}`
  const migrationURL = new URL(dbURL); migrationURL.username = migrator; migrationURL.password = 'test-only'
  owner = new pg.Client({ connectionString: migrationURL.toString() }); await owner.connect()
  assert.equal((await owner.query('SELECT current_user AS name')).rows[0].name, schemaOwner)
  await owner.query('REVOKE ALL ON SCHEMA public FROM PUBLIC')
  await owner.query(await readFile(join(output, 'migration.sql'), 'utf8'))
  await owner.query('BEGIN'); await owner.query(await readFile(join(output, 'fixture.sql'), 'utf8')); await owner.query('COMMIT')
  await owner.query(`GRANT USAGE ON SCHEMA api TO "${role}"; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA api TO "${role}";
    ALTER DEFAULT PRIVILEGES IN SCHEMA api GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO "${role}";
    ALTER DEFAULT PRIVILEGES IN SCHEMA api GRANT USAGE,SELECT ON SEQUENCES TO "${role}"`)
  const preview = await permissions(migrationURL.toString(), { role })
  assert.deepEqual(preview.blockers, []); assert.deepEqual(preview.missing, ['place_order_v2', 'read_place_order_receipt_v2'])
  await permissions(migrationURL.toString(), { role, apply: true })
  const loginURL = new URL(dbURL); loginURL.username = role; loginURL.password = 'test-only'
  const snapshot = await inspect(loginURL.toString(), ['api'])
  await writeFile(join(output, 'database.types.ts'), generateValidators(snapshot))
  await mkdir(join(output, 'node_modules/@gregale'), { recursive: true })
  await symlink(resolve('sdk/data'), join(output, 'node_modules/@gregale/data'), 'dir')
  await symlink(join(process.env.DATA_API_RUNTIME_DIR, 'node_modules/pg'), join(output, 'node_modules/pg'), 'dir')
  await writeFile(join(output, 'inference.ts'), `import { transactionClient, persistentTransactionClient, transactionMatches, reconcileMutation, type TransactionReceipt } from './client.js'
async function verify(client: ReturnType<typeof transactionClient>) {
 const pending=client.prepare({skus:['a_widget'],quantities:[2]})
 const result=reconcileMutation(await client.lookup(pending),transactionMatches(pending))
 if(result.outcome==='confirmed') {
  const receipt:TransactionReceipt=result.row; const price:number=result.row.lines[0].unit_price_cents
  // @ts-expect-error unvalidated fields remain unavailable
  result.row.lines[0].private
  void [receipt,price]
 }
}
async function verifyPersistent(client: ReturnType<typeof persistentTransactionClient>) {
 const staged=await client.prepare('checkout',{skus:['a_widget'],quantities:[1]})
 const sent=await client.execute(staged,{timeoutMs:5000})
 const result=await client.reconcile(sent.entry,sent.response)
 if(result.result.outcome==='confirmed') {const receipt:TransactionReceipt=result.result.row;void receipt}
 const restored=await client.load('checkout')
 if(restored) {const checked=await client.recover(restored,{timeoutMs:5000});if(checked.result.outcome==='confirmed'){const price:number=checked.result.row.lines[0].unit_price_cents;void price}}
}
void [verify,verifyPersistent]
`)
  await writeFile(join(output, 'tsconfig.json'), JSON.stringify({ compilerOptions: { target: 'ES2022', module: 'NodeNext', moduleResolution: 'NodeNext', strict: true, skipLibCheck: true, outDir: 'dist' }, include: ['*.ts'] }))
  await execute(resolve('sdk/data/node_modules/.bin/tsc'), ['-p', join(output, 'tsconfig.json')], { timeout: 45000 })
  const { transactionClient, persistentTransactionClient, transactionMatches, transactionArguments } = await import(pathToFileURL(join(output, 'dist/client.js')))
  const { cleanup, options } = await import(pathToFileURL(join(output, 'cleanup-receipts.mjs')))
  if (process.env.DATA_API_BROWSER_REQUIRED === '1') assert.ok(process.env.DATA_API_CHROMIUM_BIN && process.env.DATA_API_STARTER_DIR, 'browser journal acceptance requires Chromium and starter tooling')
  if (process.env.DATA_API_CHROMIUM_BIN) browser = await mutationJournalBrowserOrigin(output)
  const config = runtimeConfig({ DATA_API_ALLOWED_ORIGINS: browser?.url ?? '', DATABASE_URL: loginURL.toString(), DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'orders' })
  config.snapshot = snapshot; config.functions = snapshot.functions; config.fingerprint = fingerprint(snapshot)
  const upstream = await port(), ready = await port()
  child = spawn(process.env.DATA_API_POSTGREST_BIN, [], { env: { ...process.env, ...config.postgrestEnv, PGRST_SERVER_PORT: String(upstream), PGRST_ADMIN_SERVER_PORT: String(ready) }, stdio: ['ignore', 'pipe', 'pipe'] })
  child.stdout.on('data', () => {}); child.stderr.on('data', () => {})
  const { privateKey, publicKey } = await generateKeyPair('ES256'), jwk = await exportJWK(publicKey)
  gateway = createServer(config, tokenVerifier(config.auth, createLocalJWKSet({ keys: [jwk] })), upstream, ready, () => {})
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve)); const url = `http://127.0.0.1:${gateway.address().port}`
  for (let i = 0; i < 100 && (await fetch(url + '/healthz')).status !== 200; i++) await new Promise(resolve => setTimeout(resolve, 50))
  assert.equal((await fetch(url + '/healthz')).status, 200)
  const sign = async (sub, tenant) => new SignJWT({ sub, ...(tenant ? { tenant_id: tenant } : {}) }).setProtectedHeader({ alg: 'ES256' }).setIssuer(config.auth.issuer).setAudience(config.auth.audience).setExpirationTime('5m').sign(privateKey)
  const token = await sign('alice', 'alpha'), bobToken = await sign('bob', 'beta'), siblingToken = await sign('charlie', 'alpha')
  const alice = transactionClient({ url, accessToken: token, subject: 'alice', tenant: 'alpha' })
  const db = createDataClient({ url, accessToken: token }).schema('api'), bob = createDataClient({ url, accessToken: bobToken }).schema('api'), sibling = createDataClient({ url, accessToken: siblingToken }).schema('api')
  const cross = createDataClient({ url, accessToken: await sign('alice', 'beta') }).schema('api')
  const args = pending => ({ request_key: pending.key, skus: [...pending.skus], quantities: [...pending.quantities] })
  const write = pending => dataRequest(alice.execute(pending), { timeoutMs: 5000 }), read = pending => dataRequest(alice.lookup(pending), { timeoutMs: 2000 })
  const prepare = (skus = ['a_widget'], quantities = [1], key) => alice.prepare({ skus, quantities }, key)
  const failure = (response, status, message) => { assert.equal(response.status, status, JSON.stringify(response.error)); assert.equal(response.error?.message, message) }
  const now = async () => Number((await owner.query('SELECT floor(extract(epoch FROM clock_timestamp())*1000)::text AS ms')).rows[0].ms)
  const clean = async extra => cleanup(migrationURL.toString(), { before: new Date(await now()).toISOString(), apply: true, ...extra })
  // The stable owner obeys FORCE RLS on business tables, including rehearsal edits.
  await owner.query("SELECT set_config('request.jwt.claims',$1,false),set_config('gregale.transaction_place_order_v2','alpha',false)", [JSON.stringify({ sub: 'alice', tenant_id: 'alpha' })])
  const stock = async sku => (await owner.query("SELECT stock FROM api.place_order_v2_inventory WHERE tenant_id='alpha' AND sku=$1", [sku])).rows[0].stock
  const restock = async (sku, quantity = 10) => owner.query("UPDATE api.place_order_v2_inventory SET stock=$2 WHERE tenant_id='alpha' AND sku=$1", [sku, quantity])
  const count = async table => (await owner.query(`SELECT count(*)::int AS n FROM api.${table}`)).rows[0].n

  t.diagnostic('generated client validation and context')
  const source = { skus: ['z_widget', 'a_widget'], quantities: [1, 2] }, pending = alice.prepare(source)
  source.skus.reverse(); source.quantities[0] = 999
  assert.deepEqual(pending.skus, ['a_widget', 'z_widget']); assert.deepEqual(pending.quantities, [2, 1])
  assert.ok(Object.isFrozen(pending) && Object.isFrozen(pending.skus) && Object.isFrozen(pending.quantities))
  assert.equal(alice.prepare({ skus: pending.skus, quantities: pending.quantities }, pending.key.toUpperCase()).key, pending.key)
  let fakeCalls = 0
  const fake = transactionClient({ url, subject: 'alice', tenant: 'alpha', accessToken: token, fetch: async () => { fakeCalls++; return new Response('[]') } })
  for (const input of [{}, { skus: [], quantities: [] }, { skus: ['a_widget', 'a_widget'], quantities: [1, 1] }, { skus: new Array(1), quantities: [1] }, { skus: ['a_widget'], quantities: [1.5] }, { skus: ['a_widget'], quantities: [1001] }, { skus: ['a_widget'], quantities: [1], tenant_id: 'beta' }]) assert.throws(() => fake.prepare(input))
  assert.equal(fakeCalls, 0)
  assert.throws(() => transactionArguments.parse({ ...args(pending), tenant_id: 'beta' }))
  assert.throws(() => fake.execute({ ...pending, subject: 'bob' }))
  assert.throws(() => fake.execute({ ...pending, contract: 'place_order' }))
  assert.throws(() => fake.lookup({ ...pending, apiURL: 'https://other.example' }))
  assert.throws(() => fake.lookup({ ...pending, schemaFingerprint: 'a'.repeat(64) }))
  assert.throws(() => prepare(['a_widget'], [1], randomUUID()))
  assert.equal(fakeCalls, 0)
  assert.throws(() => alice.restore({ ...pending, accessToken: 'private field sentinel' }))
  assert.throws(() => alice.restore({ ...pending, skus: [...pending.skus].reverse(), quantities: [...pending.quantities].reverse() }))

  for (const input of [{ skus: [], quantities: [] }, { skus: ['a_widget', 'a_widget'], quantities: [1, 1] }, { skus: ['bad/sku'], quantities: [1] }, { skus: ['a_widget'], quantities: [0] }, { skus: ['a_widget'], quantities: [1001] }, { skus: null, quantities: [1] }, { skus: ['a_widget'], quantities: null }, { skus: ['a_widget'], quantities: [1, 2] }, { skus: Array.from({ length: 101 }, (_, index) => 'sku' + index), quantities: Array(101).fill(1) }]) {
    const key = prepare().key
    for (const rpc of ['place_order_v2', 'read_place_order_receipt_v2']) failure(await db.rpc(rpc, { request_key: key, ...input }).retry(false), 400, 'transaction_arguments_invalid')
    assert.equal((await owner.query('SELECT count(*)::int AS n FROM api.place_order_v2_receipts WHERE request_key=$1', [key])).rows[0].n, 0)
  }

  t.diagnostic('atomic writes, canonical replay and immutable snapshots')
  const shortage = prepare(['a_widget', 'z_widget'], [2, 2])
  failure(await write(shortage), 409, 'transaction_inventory_unavailable')
  assert.equal(await stock('a_widget'), 10); assert.equal(await count('place_order_v2_orders'), 0); assert.equal(await count('place_order_v2_items'), 0)
  assert.equal((await read(shortage)).data.length, 0)
  const concurrent = await Promise.all(Array.from({ length: 6 }, () => write(pending)))
  for (const result of concurrent) { assert.equal(result.error, null); assert.deepEqual(result.data, concurrent[0].data) }
  const saved = concurrent[0].data[0], matches = transactionMatches(pending)
  assert.equal(reconcileMutation(concurrent[0], matches).outcome, 'confirmed'); assert.equal(saved.total_cents, 1200)
  assert.equal(await stock('a_widget'), 8); assert.equal(await stock('z_widget'), 0)
  const reordered = await db.rpc('place_order_v2', { request_key: pending.key, skus: ['z_widget', 'a_widget'], quantities: [1, 2] }).retry(false)
  assert.deepEqual(reordered.data, [saved])
  failure(await write(alice.prepare({ skus: ['a_widget'], quantities: [3] }, pending.key)), 409, 'idempotency_payload_conflict')
  failure(await read(alice.prepare({ skus: ['a_widget'], quantities: [3] }, pending.key)), 409, 'idempotency_payload_conflict')
  for (const other of [bob, sibling, cross]) assert.deepEqual((await other.rpc('read_place_order_receipt_v2', { ...args(pending), quantities: [9, 9] }).retry(false)).data, [])
  for (const other of [bob, sibling, cross]) assert.equal((await other.rpc('place_order_v2', { request_key: pending.key, skus: ['a_widget'], quantities: [1] }).retry(false)).error, null)
  const noIdentity = createDataClient({ url, accessToken: await sign('outsider') }).schema('api')
  failure(await noIdentity.rpc('place_order_v2', args(prepare())).retry(false), 403, 'transaction_identity_required')
  await owner.query("UPDATE api.place_order_v2_inventory SET unit_price_cents=700 WHERE tenant_id='alpha' AND sku='a_widget'")
  await owner.query('DELETE FROM api.place_order_v2_items WHERE order_id=$1', [saved.order_id]); await owner.query('DELETE FROM api.place_order_v2_orders WHERE id=$1', [saved.order_id])
  assert.deepEqual((await write(pending)).data, [saved]); assert.deepEqual((await read(pending)).data, [saved])
  for (const alter of [row => row.lines.pop(), row => row.lines.reverse(), row => row.lines[0].quantity++, row => row.total_cents++, row => row.created_by = 'bob', row => row.lines[0].unit_price_cents = -1]) {
    const malformed = structuredClone(saved); alter(malformed)
    assert.equal(reconcileMutation({ data: [malformed], error: null, status: 200 }, matches).reason, 'invalid_receipt')
  }

  t.diagnostic('stock races, rollback and lost-response recovery')
  await restock('z_widget', 1)
  const competed = await Promise.all([write(prepare(['z_widget'])), write(prepare(['z_widget']))])
  assert.deepEqual(competed.map(result => result.status).sort(), [200, 409]); assert.equal(await stock('z_widget'), 0)
  await restock('a_widget'); await restock('z_widget')
  const inverse = await Promise.all([db.rpc('place_order_v2', args(prepare(['z_widget', 'a_widget'], [1, 1]))).retry(false), db.rpc('place_order_v2', args(prepare(['a_widget', 'z_widget'], [1, 1]))).retry(false)])
  assert.ok(inverse.every(result => result.error === null)); assert.equal(await stock('a_widget'), 8); assert.equal(await stock('z_widget'), 8)
  const race = prepare(), changedRace = alice.prepare({ skus: ['a_widget'], quantities: [2] }, race.key)
  const raced = await Promise.all([write(race), write(changedRace)])
  assert.deepEqual(raced.map(result => result.status).sort(), [200, 409])
  failure(raced.find(result => result.error), 409, 'idempotency_payload_conflict')
  assert.equal(await stock('a_widget'), 8 - raced.find(result => !result.error).data[0].lines[0].quantity)
  await restock('a_widget', 8)
  const beforeOrders = await count('place_order_v2_orders'), beforeItems = await count('place_order_v2_items'), denial = prepare()
  await owner.query('CREATE POLICY deny_test_receipt ON api.place_order_v2_receipts AS RESTRICTIVE FOR INSERT WITH CHECK (false)')
  try { assert.equal((await write(denial)).error.code, '42501'); assert.equal(await stock('a_widget'), 8); assert.equal(await count('place_order_v2_orders'), beforeOrders); assert.equal(await count('place_order_v2_items'), beforeItems); assert.deepEqual((await read(denial)).data, []) }
  finally { await owner.query('DROP POLICY deny_test_receipt ON api.place_order_v2_receipts') }
  assert.equal((await write(denial)).error, null)
  let lostCalls = 0
  const lossy = transactionClient({ url, subject: 'alice', tenant: 'alpha', accessToken: token, fetch: async (...args) => { lostCalls++; const response = await fetch(...args); await response.arrayBuffer(); throw new TypeError('lost after commit') } })
  const lost = lossy.prepare({ skus: ['a_widget'], quantities: [1] })
  assert.equal((await dataRequest(lossy.execute(lost), { timeoutMs: 5000 })).status, 0); assert.equal(lostCalls, 1)
  assert.equal(reconcileMutation(await read(lost), transactionMatches(lost)).outcome, 'confirmed')

  t.diagnostic('read-only isolation, scope restoration and direct denial')
  direct = new pg.Client({ connectionString: loginURL.toString() }); lock = new pg.Client({ connectionString: migrationURL.toString() }); await direct.connect(); await lock.connect()
  const inFlight = prepare()
  await direct.query('BEGIN'); await direct.query("SELECT set_config('request.jwt.claims',$1,true),set_config('gregale.transaction_place_order_v2','before_business',true),set_config('gregale.transaction_place_order_v2_receipt','before_receipt',true)", [JSON.stringify({ sub: 'alice', tenant_id: 'alpha' })])
  await direct.query('SELECT * FROM api.place_order_v2($1,$2,$3)', [inFlight.key, inFlight.skus, inFlight.quantities])
  assert.deepEqual((await read(inFlight)).data, [])
  assert.deepEqual((await direct.query("SELECT current_setting('gregale.transaction_place_order_v2',true) AS business,current_setting('gregale.transaction_place_order_v2_receipt',true) AS receipt")).rows[0], { business: 'before_business', receipt: 'before_receipt' })
  await direct.query('COMMIT')
  await direct.query('BEGIN READ ONLY'); await direct.query("SELECT set_config('request.jwt.claims',$1,true),set_config('gregale.transaction_place_order_v2_receipt','before_read',true)", [JSON.stringify({ sub: 'alice', tenant_id: 'alpha' })])
  assert.equal((await direct.query('SELECT * FROM api.read_place_order_receipt_v2($1,$2,$3)', [pending.key, pending.skus, pending.quantities])).rows[0].order_id, saved.order_id)
  assert.equal((await direct.query("SELECT current_setting('gregale.transaction_place_order_v2_receipt',true) AS scope")).rows[0].scope, 'before_read')
  assert.deepEqual((await direct.query('SELECT * FROM api.place_order_v2_receipts')).rows, []); await direct.query('ROLLBACK')
  assert.deepEqual((await db.from('place_order_v2_receipts').select().retry(false)).data, [])
  assert.equal((await db.from('place_order_v2_receipts').insert({ tenant_id: 'alpha', created_by: 'alice', request_key: prepare().key, request: [], response: {} }).retry(false)).error.code, '42501')
  for (const action of [db.from('place_order_v2_receipts').update({ response: {} }), db.from('place_order_v2_receipts').delete()]) assert.deepEqual((await action.eq('request_key', pending.key).select().retry(false)).data, [])
  assert.deepEqual((await db.from('place_order_v2_inventory').update({ stock: 999 }).eq('sku', 'a_widget').select().retry(false)).data, [])
  assert.equal((await db.from('place_order_v2_orders').insert({ tenant_id: 'alpha', created_by: 'alice', request_key: prepare().key }).retry(false)).error.code, '42501')
  await assert.rejects(owner.query('DELETE FROM api.place_order_v2_receipts WHERE request_key=$1', [pending.key]), error => error.message === 'idempotency_replay_window_open')
  await assert.rejects(owner.query("UPDATE api.place_order_v2_receipts SET response='{}' WHERE request_key=$1", [pending.key]), error => error.message === 'idempotency_receipt_immutable')

  t.diagnostic('expiry, bounded cleanup and late retries')
  const before = new Date(await now()).toISOString()
  assert.deepEqual(options(['--before', before]), { before, batchSize: 100, maxBatches: 1, apply: false })
  for (const input of [[], ['--before', before, '--batch-size', '0'], ['--before', before, '--max-batches', '1.5'], ['--before', before, '--apply', '--apply'], ['--before', '2026-02-30T00:00:00.000Z']]) assert.throws(() => options(input))
  assert.equal((await clean()).deleted, '0')
  await assert.rejects(cleanup(loginURL.toString(), { before }), /Receipt table owner required/)
  await assert.rejects(clean({ before: new Date(await now() + 3600000).toISOString() }), /Future cleanup cutoff refused/)
  await owner.query('ALTER TABLE api.place_order_v2_receipts FORCE ROW LEVEL SECURITY')
  try { await assert.rejects(clean(), /Receipt table owner required/) } finally { await owner.query('ALTER TABLE api.place_order_v2_receipts NO FORCE ROW LEVEL SECURITY') }
  for (const badKey of [randomUUID(), keyAt(await now() + 360000)]) failure(await db.rpc('place_order_v2', { request_key: badKey, skus: ['a_widget'], quantities: [1] }).retry(false), 400, badKey[14] === '7' ? 'idempotency_key_in_future' : 'idempotency_key_invalid')
  const expired = prepare(['a_widget'], [1], keyAt(await now() - 86400000)); failure(await write(expired), 410, 'idempotency_key_expired'); failure(await read(expired), 410, 'idempotency_key_expired')
  const waitPast = async deadline => { const delay = deadline - await now() + 30; if (delay > 0) await new Promise(resolve => setTimeout(resolve, delay)) }
  const waitForBlock = async pid => {
    for (let i = 0; i < 100; i++) {
      if ((await admin.query('SELECT count(*)::int AS n FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid))', [pid])).rows[0].n) return
      await new Promise(resolve => setTimeout(resolve, 10))
    }
    assert.fail('expected transaction lock wait was not observed')
  }
  const deadline = await now() + 2200, near = prepare(['a_widget'], [1], keyAt(deadline - 86400000)), nearResponse = await write(near)
  assert.equal(nearResponse.error, null)
  await lock.query('BEGIN'); const pid = (await lock.query('SELECT pg_backend_pid() AS pid')).rows[0].pid
  await lock.query("SELECT pg_advisory_xact_lock(hashtextextended(jsonb_build_array('gregale.transaction.place_order.v2','alpha','alice',$1::uuid)::text,0))", [near.key])
  const waiting = write(near); await waitForBlock(pid); await waitPast(deadline)
  const skipped = await clean(); assert.equal(skipped.eligible, '1'); assert.equal(skipped.deleted, '0')
  await lock.query('ROLLBACK'); failure(await waiting, 410, 'idempotency_key_expired')
  assert.deepEqual((await read(near)).data, nearResponse.data)
  await lock.query('BEGIN'); await lock.query('SELECT 1 FROM api.place_order_v2_receipts WHERE request_key=$1 FOR UPDATE', [near.key]); assert.equal((await clean()).deleted, '0'); await lock.query('ROLLBACK')
  assert.equal((await clean({ apply: false })).eligible, '1')
  const businessCount = await count('place_order_v2_orders'), priorStock = await stock('a_widget')
  const purge = await clean({ batchSize: 1, maxBatches: 1 }); assert.equal(purge.deleted, '1'); assert.equal(purge.remaining, '0')
  failure(await write(near), 410, 'idempotency_key_expired'); failure(await read(near), 410, 'idempotency_key_expired')
  assert.equal(await stock('a_widget'), priorStock); assert.equal(await count('place_order_v2_orders'), businessCount)
  assert.equal(reconcileMutation(await read(near), transactionMatches(near)).reason, 'replay_expired')

  await lock.query('BEGIN'); await lock.query("SELECT set_config('request.jwt.claims',$1,true),set_config('gregale.transaction_place_order_v2','alpha',true)", [JSON.stringify({ sub: 'alice', tenant_id: 'alpha' })]); await lock.query("SELECT 1 FROM api.place_order_v2_inventory WHERE tenant_id='alpha' AND sku='a_widget' FOR UPDATE")
  const workDeadline = await now() + 1500, blocked = prepare(['a_widget'], [1], keyAt(workDeadline - 86400000)), working = write(blocked)
  await waitForBlock(pid); await waitPast(workDeadline); await lock.query('ROLLBACK'); failure(await working, 410, 'idempotency_key_expired')
  assert.equal(await stock('a_widget'), priorStock); assert.equal(await count('place_order_v2_orders'), businessCount)

  // Expiry during DML also rolls back completed earlier statements.
  await owner.query("CREATE FUNCTION api.test_tx_v2_delay() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(1); RETURN NEW; END $$; CREATE TRIGGER test_tx_v2_delay AFTER UPDATE ON api.place_order_v2_inventory FOR EACH ROW EXECUTE FUNCTION api.test_tx_v2_delay()")
  try {
    const during = prepare(['a_widget'], [1], keyAt(await now() + 500 - 86400000)), started = performance.now()
    failure(await write(during), 410, 'idempotency_key_expired'); assert.ok(performance.now() - started >= 900, 'expiry must be observed after the delayed mutation'); assert.equal(await stock('a_widget'), priorStock); assert.equal(await count('place_order_v2_orders'), businessCount)
  } finally { await owner.query('DROP TRIGGER test_tx_v2_delay ON api.place_order_v2_inventory; DROP FUNCTION api.test_tx_v2_delay()') }
  const checks = JSON.parse(await readFile(join(output, 'data-checks.json'), 'utf8')); for (const check of checks.checks) assert.equal((await owner.query(check.sql)).rows[0].passed, true)

  // Saved access checks intentionally remain valid regardless of wall clock.
  await owner.query('TRUNCATE api.place_order_v2_items,api.place_order_v2_orders,api.place_order_v2_inventory')
  await owner.query('BEGIN'); await owner.query(await readFile(join(output, 'fixture.sql'), 'utf8')); await owner.query('COMMIT')
  const { prepareAccessChecks, runAccessChecks } = await import(new URL('dev-access.mjs', runtime))
  const scenarios = await prepareAccessChecks(join(output, 'access-checks.json'), { identities: { alice: {}, bob: {} } })
  const checked = await runAccessChecks(scenarios, { url, identities: { alice: { token }, bob: { token: bobToken } }, signal: AbortSignal.timeout(30000) })
  assert.ok(checked.every(result => result.passed), JSON.stringify(checked))
  t.diagnostic('persistent generated client, fresh handles and actual read-only receipt recovery')
  const records = new Map(), store = { async get(slot) { return records.get(slot) ?? null }, async compareExchange(slot, expected, next) {
    if ((records.get(slot) ?? null) !== expected) return false
    if (next === null) records.delete(slot); else records.set(slot, next)
    return true
  } }
  let sends = 0
  const persistentOptions = { url, subject: 'alice', tenant: 'alpha', accessToken: token, fetch: async (...args) => {
    sends++; const path = new URL(String(args[0])).pathname
    if (path.endsWith('/rpc/place_order_v2')) assert.equal(JSON.parse(records.get('checkout')).state, 'uncertain')
    return fetch(...args)
  } }
  const persistent = persistentTransactionClient(persistentOptions, store)
  const staged = await persistent.prepare('checkout', { skus: ['a_widget'], quantities: [1] })
  assert.equal(sends, 0)
  const sent = await persistent.execute(staged, { timeoutMs: 5000 })
  assert.equal(sent.response.error, null); assert.equal(sends, 1)
  const restarted = persistentTransactionClient(persistentOptions, store), restored = await restarted.load('checkout')
  assert.equal(restored.state, 'uncertain'); assert.equal(restored.operation.key, staged.operation.key); assert.equal(sends, 1)
  const recovered = await restarted.recover(restored, { timeoutMs: 5000 })
  assert.equal(recovered.result.outcome, 'confirmed'); assert.equal(recovered.persistence, 'saved'); assert.equal(sends, 2)
  await restarted.removeConfirmed(recovered.entry)
  assert.equal(await restarted.load('checkout'), null)
  if (browser) {
    t.diagnostic('IndexedDB reload, cross-tab CAS, quota, corruption and actual receipt recovery')
    await verifyMutationJournalBrowser({ origin: browser, url, token, expiredKey: keyAt(await now() - 86400000), owner })
  }
})
