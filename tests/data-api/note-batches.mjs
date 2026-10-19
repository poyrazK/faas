import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { classifyVersionedMutation, createReplayKeyV2, dataRequest, reconcileMutation } from '../../sdk/data/dist/index.js'
import { limits } from '../../cmd/gregale/templates/data-api/config.mjs'

const keyAt = milliseconds => {
  const time = Math.floor(milliseconds).toString(16).padStart(12, '0')
  return `${time.slice(0, 8)}-${time.slice(8)}-7${randomUUID().slice(15)}`
}

export async function verifyNoteBatches({ owner, client, root, pg, migrationURL, loginURL, userA, userB, url, makeClient }) {
  const { cleanup } = await import(pathToFileURL(join(root, 'migrations/cleanup-receipts.mjs')))
  const { prepareNoteBatch, noteBatchMatches } = await import(pathToFileURL(join(root, 'client/dist/note-batch.js')))
  const ids = [], rejectedKeys = []
  const bob = makeClient({ url, subject: userB.subject, accessToken: userB.token })
  const make = async (body, api = client) => {
    const response = await api.create('batch-' + body)
    assert.equal(response.error, null); ids.push(response.data.id); return response.data
  }
  const update = (row, patch = { priority: 3 }) => ({ operation: 'update', note_id: row.id, expected_version: row.version, patch })
  const remove = row => ({ operation: 'delete', note_id: row.id, expected_version: row.version })
  const apply = (key, operations, api = client) => dataRequest(api.applyBatchOnceV2(key, operations), { timeoutMs: 5000 })
  const read = (key, operations, api = client) => dataRequest(api.readBatchReceiptV2(key, operations), { timeoutMs: 2000 })
  const conflict = (response, message = 'idempotency_version_conflict') => {
    assert.equal(response.status, 409, JSON.stringify(response.error)); assert.equal(response.error?.code, 'PT409'); assert.equal(response.error.message, message)
  }
  const expired = response => { assert.equal(response.status, 410, JSON.stringify(response.error)); assert.equal(response.error.message, 'idempotency_key_expired') }
  const now = async () => Number((await owner.query('SELECT floor(extract(epoch FROM clock_timestamp())*1000)::text AS ms')).rows[0].ms)
  const clean = async extra => cleanup(migrationURL, { contract: 'v2', receiptKind: 'batch', before: new Date(await now()).toISOString(), apply: true, ...extra })
  const lock = new pg.Client({ connectionString: migrationURL }), direct = new pg.Client({ connectionString: loginURL })
  await lock.connect(); await direct.connect()
  const waitForBlock = async pid => {
    for (let i = 0; i < 100; i++) {
      if ((await owner.query('SELECT count(*)::int AS n FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid))', [pid])).rows[0].n) return
      await new Promise(resolve => setTimeout(resolve, 10))
    }
    assert.fail('batch did not enter the expected lock wait')
  }
  const waitPast = async deadline => { const delay = deadline - await now() + 30; if (delay > 0) await new Promise(resolve => setTimeout(resolve, delay)) }
  try {
    const first = await make('first'), second = await make('second'), key = createReplayKeyV2()
    const operations = [remove(second), update(first, { body: 'batch-updated', priority: 7 })]
    const pending = prepareNoteBatch(userA.subject, operations, key)
    const concurrent = await Promise.all(Array.from({ length: 6 }, () => apply(key, operations)))
    for (const result of concurrent) { assert.equal(result.error, null); assert.deepEqual(result.data, concurrent[0].data) }
    const envelope = concurrent[0].data[0]
    assert.equal(classifyVersionedMutation(concurrent[0], noteBatchMatches(pending)).outcome, 'applied')
    assert.deepEqual(envelope.results.map(item => [item.index, item.operation, item.note.id, item.note.version]), [[0, 'delete', second.id, 1], [1, 'update', first.id, 2]])
    assert.equal((await owner.query('SELECT count(*)::int AS n FROM api.note_batch_receipts_v2 WHERE request_key=$1', [key])).rows[0].n, 1)
    conflict(await apply(key, [...operations].reverse()), 'idempotency_payload_conflict')
    conflict(await read(key, [operations[0], update(first, { priority: 8 })]), 'idempotency_payload_conflict')
    await client.update(first.id, 2, { body: 'later' }); await client.remove(first.id)
    assert.deepEqual((await apply(key, operations)).data, [envelope], 'replay returns original snapshots after later edits and deletion')
    assert.equal(reconcileMutation(await read(key, operations), noteBatchMatches(pending)).outcome, 'confirmed')
    assert.deepEqual((await read(key, operations, bob)).data, [])
    const bobNote = await make('bob', bob)
    assert.equal((await apply(key, [update(bobNote)], bob)).data[0].subject, userB.subject, 'subjects have separate key namespaces')

    // Every precondition is checked under row locks before any changes. Missing,
    // stale and hidden rows have the same response and never reserve the key.
    const preserved = await make('preserved'), stale = await make('stale')
    await client.update(stale.id, 1, { priority: 1 })
    for (const unavailable of [stale, bobNote, { id: 2147483647, version: 1 }]) {
      const rejectedKey = createReplayKeyV2(); rejectedKeys.push(rejectedKey)
      conflict(await apply(rejectedKey, [update(preserved), remove(unavailable)]))
      assert.equal((await owner.query('SELECT version FROM api.notes WHERE id=$1', [preserved.id])).rows[0].version, 1)
    }
    const invalidItems = [null, [], {}, { ...remove(preserved), patch: null }, { ...remove(preserved), subject: userA.subject },
      { ...remove(preserved), note_id: '1' }, { ...remove(preserved), expected_version: 1.5 },
      update(preserved, {}), update(preserved, { body: null }), update(preserved, { version: 2 }),
      update(preserved, { priority: 2147483648 }), update(preserved, { priority: 1.5 })]
    for (const input of [null, {}, [], [remove(preserved), remove(preserved)], Array.from({ length: 101 }, (_, id) => remove({ id: id + 1, version: 1 })), ...invalidItems.map(item => [update(preserved), item])]) {
      const rejectedKey = createReplayKeyV2(); rejectedKeys.push(rejectedKey)
      const result = await client.db.rpc('apply_note_batch_once_v2', { idempotency_key: rejectedKey, operations: input }).retry(false)
      assert.equal(result.status, 400, JSON.stringify(result.error)); assert.equal(result.error.message, 'idempotency_batch_invalid')
      const lookup = await client.db.rpc('read_note_batch_receipt_v2', { idempotency_key: rejectedKey, operations: input }).retry(false)
      assert.equal(lookup.status, 400); assert.equal(lookup.error.message, 'idempotency_batch_invalid')
    }
    assert.equal((await owner.query('SELECT count(*)::int AS n FROM api.note_batch_receipts_v2 WHERE request_key=ANY($1::uuid[])', [rejectedKeys])).rows[0].n, 0)
    assert.equal((await owner.query('SELECT version FROM api.notes WHERE id=$1', [preserved.id])).rows[0].version, 1)
    assert.equal((await apply(rejectedKeys[0], [update(preserved)])).error, null, 'failed batches leave the key available for an explicit corrected operation')

    const hundred = await client.createMany(Array.from({ length: 100 }, (_, index) => ({ body: `batch-bound-${index}` })))
    assert.equal(hundred.error, null); ids.push(...hundred.data.map(row => row.id))
    const atLimit = await apply(createReplayKeyV2(), hundred.data.map(row => update(row)))
    assert.equal(atLimit.error, null); assert.equal(atLimit.data[0].results.length, 100)

    // Opposite submitted orders acquire business row locks in the same ID order.
    for (const shared of [false, true]) {
      const a = await make('race-a'), b = await make('race-b'), raceKey = createReplayKeyV2()
      const raced = await Promise.all([apply(raceKey, [update(b), update(a)]), apply(shared ? raceKey : createReplayKeyV2(), [remove(a), remove(b)])])
      assert.deepEqual(raced.map(result => result.status).sort(), [200, 409])
      conflict(raced.find(result => result.error), shared ? 'idempotency_payload_conflict' : 'idempotency_version_conflict')
      const rows = (await owner.query('SELECT version FROM api.notes WHERE id=ANY($1::int[])', [[a.id, b.id]])).rows
      assert.ok(rows.length === 0 || (rows.length === 2 && rows.every(row => row.version === 2)))
    }

    const lost = await make('lost'), lostKey = createReplayKeyV2(), lostOperations = [update(lost)]; let calls = 0
    const lossy = makeClient({ url, subject: userA.subject, accessToken: userA.token, fetch: async (...args) => {
      calls++; const response = await fetch(...args); await response.arrayBuffer(); throw new TypeError('lost after batch commit')
    } })
    assert.equal((await apply(lostKey, lostOperations, lossy)).status, 0); assert.equal(calls, 1)
    assert.equal(reconcileMutation(await read(lostKey, lostOperations), noteBatchMatches(prepareNoteBatch(userA.subject, lostOperations, lostKey))).outcome, 'confirmed')

    const inflight = await make('inflight'), inflightKey = createReplayKeyV2(), inflightOps = [update(inflight)]
    await direct.query('BEGIN')
    await direct.query("SELECT set_config('request.jwt.claims',$1,true),set_config('gregale.idempotency_rpc','batch_before',true)", [JSON.stringify({ sub: userA.subject })])
    const numeric = JSON.stringify(inflightOps).replace('"priority":3', '"priority":3.0')
    assert.equal((await direct.query('SELECT * FROM api.apply_note_batch_once_v2($1,$2::jsonb)', [inflightKey, numeric])).rows.length, 1)
    assert.equal((await direct.query("SELECT current_setting('gregale.idempotency_rpc',true) AS scope")).rows[0].scope, 'batch_before')
    assert.deepEqual((await read(inflightKey, inflightOps)).data, [], 'read-only lookup never waits for or commits an in-flight write')
    await direct.query('COMMIT')
    await direct.query('BEGIN READ ONLY')
    await direct.query("SELECT set_config('request.jwt.claims',$1,true),set_config('gregale.idempotency_rpc','batch_before_read',true)", [JSON.stringify({ sub: userA.subject })])
    assert.deepEqual((await direct.query('SELECT * FROM api.read_note_batch_receipt_v2($1,$2::jsonb)', [key, JSON.stringify(operations)])).rows, [envelope])
    assert.equal((await direct.query("SELECT current_setting('gregale.idempotency_rpc',true) AS scope")).rows[0].scope, 'batch_before_read')
    assert.deepEqual((await direct.query('SELECT * FROM api.note_batch_receipts_v2')).rows, [])
    await direct.query('ROLLBACK')
    assert.deepEqual((await client.db.from('note_batch_receipts_v2').select().retry(false)).data, [])
    assert.equal((await client.db.from('note_batch_receipts_v2').insert({ subject: userA.subject, request_key: createReplayKeyV2(), request: [], response: [] }).retry(false)).error.code, '42501')
    assert.deepEqual((await client.db.from('note_batch_receipts_v2').update({ response: [] }).eq('request_key', key).select().retry(false)).data, [])
    assert.deepEqual((await client.db.from('note_batch_receipts_v2').delete().eq('request_key', key).select().retry(false)).data, [])
    await assert.rejects(owner.query('DELETE FROM api.note_batch_receipts_v2 WHERE request_key=$1', [key]), error => error.message === 'idempotency_replay_window_open')
    await assert.rejects(owner.query("UPDATE api.note_batch_receipts_v2 SET response='[]' WHERE request_key=$1", [key]), error => error.message === 'idempotency_receipt_immutable')
    assert.equal((await clean()).deleted, '0')
    await assert.rejects(cleanup(loginURL, { contract: 'v2', receiptKind: 'batch', before: new Date(await now()).toISOString() }), /Receipt table owner required/)

    // An RLS failure on a later operation or receipt insertion rolls back every
    // earlier change, including a deleted note's comment cascade.
    const rollbackA = await make('rollback-a'), rollbackB = await make('rollback-b'), rollbackOps = [remove(rollbackA), update(rollbackB, { body: 'batch-denied' })]
    assert.equal((await client.reply(rollbackA.id, 'batch-cascade')).error, null)
    const verifyRollback = async response => {
      assert.equal(response.error.code, '42501')
      const rows = (await owner.query('SELECT version FROM api.notes WHERE id=ANY($1::int[])', [[rollbackA.id, rollbackB.id]])).rows
      assert.equal(rows.length, 2); assert.ok(rows.every(row => row.version === 1))
      assert.equal((await owner.query('SELECT count(*)::int AS n FROM api.comments WHERE note_id=$1', [rollbackA.id])).rows[0].n, 1)
    }
    await owner.query("CREATE POLICY batch_deny_note ON api.notes AS RESTRICTIVE FOR UPDATE WITH CHECK (body <> 'batch-denied')")
    try { await verifyRollback(await apply(createReplayKeyV2(), rollbackOps)) }
    finally { await owner.query('DROP POLICY batch_deny_note ON api.notes') }
    await owner.query("CREATE POLICY batch_deny_receipt ON api.note_batch_receipts_v2 AS RESTRICTIVE FOR INSERT WITH CHECK (false)")
    const denialKey = createReplayKeyV2()
    try { await verifyRollback(await apply(denialKey, rollbackOps)); assert.deepEqual((await read(denialKey, rollbackOps)).data, []) }
    finally { await owner.query('DROP POLICY batch_deny_receipt ON api.note_batch_receipts_v2') }
    assert.equal((await apply(denialKey, rollbackOps)).error, null)

    // A bounded response must remain recoverable within the HTTP response cap.
    const largeA = await make('large-a'), largeB = await make('large-b')
    await owner.query("UPDATE api.notes SET body=repeat('x',600000) WHERE id=ANY($1::int[])", [[largeA.id, largeB.id]])
    const largeKey = createReplayKeyV2(), largeOps = [remove({ ...largeA, version: 2 }), update({ ...largeB, version: 2 })]
    const oversized = await apply(largeKey, largeOps)
    assert.equal(oversized.status, 413, JSON.stringify(oversized.error)); assert.equal(oversized.error.message, 'idempotency_batch_result_too_large')
    const largeRows = (await owner.query('SELECT version FROM api.notes WHERE id=ANY($1::int[])', [[largeA.id, largeB.id]])).rows
    assert.equal(largeRows.length, 2); assert.ok(largeRows.every(row => row.version === 2))
    assert.deepEqual((await read(largeKey, largeOps)).data, [])

    for (const badKey of [randomUUID(), keyAt(await now() + 360000)]) assert.equal((await apply(badKey, inflightOps)).status, 400)
    expired(await apply(keyAt(await now() - 86400000), inflightOps))
    const near = await make('near'), deadline = await now() + 2200, nearKey = keyAt(deadline - 86400000), nearOps = [update(near)]
    const nearResponse = await apply(nearKey, nearOps); assert.equal(nearResponse.error, null)
    await lock.query('BEGIN')
    const pid = (await lock.query('SELECT pg_backend_pid() AS pid')).rows[0].pid
    await lock.query("SELECT pg_advisory_xact_lock(hashtextextended(jsonb_build_array('gregale.note_batch.v2',$1::text,$2::uuid)::text,0))", [userA.subject, nearKey])
    // This request deliberately waits through expiry and the bounded owner
    // cleanup command. Give it the serving budget rather than the short client
    // timeout used by ordinary unblocked assertions below.
    const waiting = dataRequest(client.applyBatchOnceV2(nearKey, nearOps), { timeoutMs: limits.queryMs })
    await waitForBlock(pid); await waitPast(deadline)
    const skipped = await clean(); assert.equal(skipped.eligible, '1'); assert.equal(skipped.deleted, '0')
    await lock.query('ROLLBACK'); expired(await waiting)
    assert.deepEqual((await read(nearKey, nearOps)).data, nearResponse.data, 'expired retained receipt remains readable')
    await lock.query('BEGIN'); await lock.query('SELECT 1 FROM api.note_batch_receipts_v2 WHERE request_key=$1 FOR UPDATE', [nearKey])
    assert.equal((await clean()).deleted, '0'); await lock.query('ROLLBACK')
    const purge = await clean({ batchSize: 1, maxBatches: 1 }); assert.equal(purge.receiptKind, 'batch'); assert.equal(purge.deleted, '1')
    expired(await apply(nearKey, nearOps)); expired(await read(nearKey, nearOps))
    assert.equal((await owner.query('SELECT version FROM api.notes WHERE id=$1', [near.id])).rows[0].version, 2)

    const blockedA = await make('blocked-a'), blockedB = await make('blocked-b'), blockOps = [update(blockedA), remove(blockedB)]
    await lock.query('BEGIN'); await lock.query('SELECT 1 FROM api.notes WHERE id=$1 FOR UPDATE', [blockedA.id])
    const workDeadline = await now() + 1500, workKey = keyAt(workDeadline - 86400000), working = apply(workKey, blockOps)
    await waitForBlock(pid); await waitPast(workDeadline); await lock.query('ROLLBACK'); expired(await working)
    assert.equal((await owner.query('SELECT count(*)::int AS n FROM api.notes WHERE id=ANY($1::int[]) AND version=1', [[blockedA.id, blockedB.id]])).rows[0].n, 2)
    expired(await read(workKey, blockOps))
  } finally {
    await lock.query('ROLLBACK'); await direct.query('ROLLBACK'); await lock.end(); await direct.end()
    await owner.query('DELETE FROM api.notes WHERE id=ANY($1::int[])', [ids])
  }
}
