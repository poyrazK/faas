import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { pathToFileURL } from 'node:url'
import { join } from 'node:path'
import { classifyDataError, createReplayKeyV2, dataRequest, reconcileMutation } from '../../sdk/data/dist/index.js'

// Tests deliberately construct past/future UUIDv7 timestamps. Production callers
// capture createReplayKeyV2() once and never rewrite an operation's key.
function keyAt(milliseconds) {
  const timestamp = Math.floor(milliseconds).toString(16).padStart(12, '0')
  return `${timestamp.slice(0, 8)}-${timestamp.slice(8)}-7${randomUUID().slice(15)}`
}

export async function verifyReplayExpiry({ owner, client, root, pg, migrationURL, loginURL, userA, userB, url }) {
  const { cleanup } = await import(pathToFileURL(join(root, 'migrations/cleanup-receipts.mjs')))
  const now = async () => Number((await owner.query('SELECT floor(extract(epoch FROM clock_timestamp())*1000)::text AS ms')).rows[0].ms)
  const create = (key, body = 'expiry-live', tags = [], db = client.db) => dataRequest(db.rpc('create_note_with_tags_once_v2', { idempotency_key: key, note_body: body, tag_names: tags }), { timeoutMs: 5000 })
  const lookup = (key, body = 'expiry-live', tags = [], db = client.db) => dataRequest(db.rpc('read_note_create_receipt_v2', { idempotency_key: key, note_body: body, tag_names: tags }), { timeoutMs: 2000 })
  const expired = result => {
    assert.equal(result.status, 410, JSON.stringify(result.error))
    assert.equal(result.error.code, 'PT410'); assert.equal(result.error.message, 'idempotency_key_expired')
    assert.equal(classifyDataError(result.error).category, 'replay_expired')
  }
  const before = async () => new Date(await now()).toISOString()
  const cleanupOptions = async () => ({ before: await before(), contract: 'v2', batchSize: 100, maxBatches: 1, apply: true })
  const waitPast = async time => {
    const wait = time - await now() + 25
    assert.ok(wait < 5000)
    if (wait > 0) await new Promise(resolve => setTimeout(resolve, wait))
  }
  const lock = new pg.Client({ connectionString: migrationURL })
  const restricted = new pg.Client({ connectionString: loginURL })
  await lock.connect(); await restricted.connect()
  try {
    const live = createReplayKeyV2()
    const concurrent = await Promise.all(Array.from({ length: 6 }, () => create(live)))
    for (const result of concurrent) { assert.equal(result.error, null); assert.deepEqual(result.data, concurrent[0].data) }
    const original = concurrent[0].data[0]
    assert.equal((await create(live, 'expiry-different')).status, 409)
    assert.deepEqual((await lookup(live)).data, [original])
    assert.equal((await lookup(live, 'expiry-different')).error.message, 'idempotency_payload_conflict')
    const bob = (await import('../../sdk/data/dist/index.js')).createDataClient({ url, accessToken: userB.token }).schema('api')
    assert.deepEqual((await lookup(live, 'expiry-different', [], bob)).data, [], 'hidden receipts do not disclose payload conflicts')
    const separate = await create(live, 'expiry-bob', [], bob)
    assert.equal(separate.error, null); assert.notEqual(separate.data[0].id, original.id)
    assert.equal(separate.data[0].subject, userB.subject)
    assert.deepEqual((await lookup(createReplayKeyV2())).data, [])
    assert.equal((await owner.query('SELECT count(*)::int AS n FROM api.note_create_receipts WHERE request_key=$1', [live])).rows[0].n, 0, 'v1 and v2 receipt namespaces stay separate')
    const saved = (await owner.query('SELECT expires_at FROM api.note_create_receipts_v2 WHERE subject=$1 AND request_key=$2', [userA.subject, live])).rows[0]
    assert.equal(saved.expires_at.getTime(), parseInt(live.replaceAll('-', '').slice(0, 12), 16) + 86400000)
    await assert.rejects(owner.query('DELETE FROM api.note_create_receipts_v2 WHERE request_key=$1', [live]), error => error.message === 'idempotency_replay_window_open')
    await assert.rejects(owner.query("UPDATE api.note_create_receipts_v2 SET response='{}' WHERE request_key=$1", [live]), error => error.message === 'idempotency_receipt_immutable')
    assert.equal((await cleanup(migrationURL, await cleanupOptions())).deleted, '0', 'cleanup cannot purge a live key')
    await assert.rejects(cleanup(loginURL, await cleanupOptions()), /Receipt table owner required/)
    await assert.rejects(cleanup(migrationURL, { ...await cleanupOptions(), before: '9999-01-01T00:00:00.000Z' }), /Future cleanup cutoff refused/)

    for (const key of [null, randomUUID(), createReplayKeyV2().slice(0, 19) + '0' + createReplayKeyV2().slice(20)]) {
      const result = await create(key, 'expiry-invalid')
      assert.equal(result.status, 400); assert.equal(result.error.message, 'idempotency_key_invalid')
    }
    const future = keyAt(await now() + 360000)
    for (const result of [await create(future, 'expiry-future'), await lookup(future, 'expiry-future')]) {
      assert.equal(result.status, 400); assert.equal(result.error.message, 'idempotency_key_in_future')
    }
    const allowedSkew = keyAt(await now() + 240000)
    assert.equal((await create(allowedSkew, 'expiry-skew')).error, null)
    // Boundary and never-used expired keys are rejected without reserving a key.
    const boundary = keyAt(await now() - 86400000)
    expired(await create(boundary, 'expiry-never-used'))
    assert.equal(reconcileMutation(await lookup(boundary, 'expiry-never-used'), () => true).reason, 'replay_expired')
    assert.equal((await owner.query("SELECT count(*)::int AS n FROM api.notes WHERE body IN ('expiry-invalid','expiry-future','expiry-never-used')")).rows[0].n, 0)
    assert.equal((await owner.query('SELECT count(*)::int AS n FROM api.note_create_receipts_v2 WHERE request_key=ANY($1::uuid[])', [[future, boundary]])).rows[0].n, 0)

    // READ ONLY lookups restore scope and cannot grant direct table access.
    await restricted.query('BEGIN READ ONLY')
    await restricted.query("SELECT set_config('request.jwt.claims',$1,true),set_config('gregale.idempotency_rpc','before_v2_lookup',true)", [JSON.stringify({ sub: userA.subject })])
    assert.equal((await restricted.query('SELECT id FROM api.read_note_create_receipt_v2($1::uuid,$2)', [live, 'expiry-live'])).rows[0].id, original.id)
    assert.equal((await restricted.query("SELECT current_setting('gregale.idempotency_rpc',true) AS scope")).rows[0].scope, 'before_v2_lookup')
    assert.deepEqual((await restricted.query('SELECT * FROM api.note_create_receipts_v2')).rows, [])
    await restricted.query('ROLLBACK')
    const hidden = await client.db.from('note_create_receipts_v2').select().retry(false)
    assert.equal(hidden.error, null); assert.deepEqual(hidden.data, [])
    assert.equal((await client.db.from('note_create_receipts_v2').insert({ subject: userA.subject, request_key: createReplayKeyV2(), request: {}, response: {} }).retry(false)).error.code, '42501')
    assert.deepEqual((await client.db.from('note_create_receipts_v2').delete().eq('request_key', live).select().retry(false)).data, [])

    // Replay entered before expiry, then waits across it. Cleanup sees the
    // expired receipt but skips the held operation lock; the waiter must fail.
    const nearDeadline = await now() + 2500, near = keyAt(nearDeadline - 86400000)
    const retained = await create(near, 'expiry-retained')
    assert.equal(retained.error, null)
    await lock.query('BEGIN')
    const pid = (await lock.query('SELECT pg_backend_pid() AS pid')).rows[0].pid
    await lock.query("SELECT pg_advisory_xact_lock(hashtextextended(jsonb_build_array('gregale.note_create.v2',$1::text,$2::uuid)::text,0))", [userA.subject, near])
    const waiting = create(near, 'expiry-retained')
    let blocked = false
    for (let i = 0; i < 80; i++) {
      blocked = (await owner.query('SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid))) AS blocked', [pid])).rows[0].blocked
      if (blocked) break
      await new Promise(resolve => setTimeout(resolve, 10))
    }
    assert.equal(blocked, true, 'replay must actually wait on its operation lock')
    await waitPast(nearDeadline)
    const skipped = await cleanup(migrationURL, await cleanupOptions())
    assert.equal(skipped.eligible, '1'); assert.equal(skipped.deleted, '0')
    await lock.query('ROLLBACK')
    expired(await waiting)
    const confirmation = await lookup(near, 'expiry-retained')
    assert.deepEqual(confirmation.data, retained.data, 'expiry does not prevent reading a retained original receipt')
    const preview = await cleanup(migrationURL, { ...await cleanupOptions(), apply: false })
    assert.equal(preview.eligible, '1'); assert.equal(preview.deleted, '0')
    const purged = await cleanup(migrationURL, await cleanupOptions())
    assert.equal(purged.deleted, '1'); assert.equal(purged.remaining, '0')
    expired(await create(near, 'expiry-retained'))
    expired(await create(near, 'expiry-replacement-payload'))
    expired(await lookup(near, 'expiry-retained'))
    assert.equal((await owner.query("SELECT count(*)::int AS n FROM api.notes WHERE body='expiry-retained'")).rows[0].n, 1, 'late retry after purge cannot duplicate the original note')
    assert.equal((await owner.query("SELECT count(*)::int AS n FROM api.notes WHERE body='expiry-replacement-payload'")).rows[0].n, 0)

    // The final wall-clock check also rolls back work waiting on business locks.
    await lock.query('BEGIN'); await lock.query('LOCK TABLE api.notes IN SHARE MODE')
    const workDeadline = await now() + 1800, workKey = keyAt(workDeadline - 86400000)
    const working = create(workKey, 'expiry-business-wait', ['expiry-business-tag'])
    blocked = false
    for (let i = 0; i < 80; i++) {
      blocked = (await owner.query('SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid))) AS blocked', [pid])).rows[0].blocked
      if (blocked) break
      await new Promise(resolve => setTimeout(resolve, 10))
    }
    assert.equal(blocked, true, 'write must actually wait on the business table')
    await waitPast(workDeadline)
    await lock.query('ROLLBACK')
    expired(await working)
    assert.equal((await owner.query("SELECT count(*)::int AS n FROM api.notes WHERE body='expiry-business-wait'")).rows[0].n, 0)
    assert.equal((await owner.query("SELECT count(*)::int AS n FROM api.tags WHERE name='expiry-business-tag'")).rows[0].n, 0)
    assert.equal((await owner.query('SELECT count(*)::int AS n FROM api.note_create_receipts_v2 WHERE request_key=$1', [workKey])).rows[0].n, 0)
  } finally {
    await lock.query('ROLLBACK'); await restricted.query('ROLLBACK')
    await lock.end(); await restricted.end()
    // Live receipts deliberately remain protected until the fixture DB is
    // dropped. Do not disable the guard just to clean up a test.
    await owner.query("DELETE FROM api.notes WHERE body LIKE 'expiry-%'")
    await owner.query("DELETE FROM api.tags WHERE name LIKE 'expiry-%'")
  }
}
