import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { classifyVersionedMutation, createReplayKeyV2, dataRequest, reconcileMutation } from '../../sdk/data/dist/index.js'

const keyAt = milliseconds => {
  const time = Math.floor(milliseconds).toString(16).padStart(12, '0')
  return `${time.slice(0, 8)}-${time.slice(8)}-7${randomUUID().slice(15)}`
}

export async function verifyNoteMutations({ owner, client, root, pg, migrationURL, loginURL, userA, userB, url, makeClient }) {
  const { cleanup } = await import(pathToFileURL(join(root, 'migrations/cleanup-receipts.mjs')))
  const ids = []
  const bob = makeClient({ url, subject: userB.subject, accessToken: userB.token })
  const make = async (body, api = client) => {
    const response = await api.create('mutation-' + body)
    assert.equal(response.error, null); ids.push(response.data.id); return response.data
  }
  const update = (key, row, patch, api = client) => dataRequest(api.updateOnceV2(key, row.id, row.version, patch), { timeoutMs: 5000 })
  const remove = (key, row, api = client) => dataRequest(api.removeOnceV2(key, row.id, row.version), { timeoutMs: 5000 })
  const readUpdate = (key, row, patch, api = client) => dataRequest(api.readUpdateReceiptV2(key, row.id, row.version, patch), { timeoutMs: 2000 })
  const readDelete = (key, row, api = client) => dataRequest(api.readDeleteReceiptV2(key, row.id, row.version), { timeoutMs: 2000 })
  const match = (id, version) => row => row.subject === userA.subject && row.id === id && row.version === version
  const conflict = (response, message = 'idempotency_version_conflict') => {
    assert.equal(response.status, 409, JSON.stringify(response.error)); assert.equal(response.error.message, message)
    assert.equal(response.error.code, 'PT409')
  }
  const expired = response => {
    assert.equal(response.status, 410, JSON.stringify(response.error)); assert.equal(response.error.message, 'idempotency_key_expired')
  }
  const now = async () => Number((await owner.query('SELECT floor(extract(epoch FROM clock_timestamp())*1000)::text AS ms')).rows[0].ms)
  const clean = async extra => cleanup(migrationURL, { contract: 'v2', receiptKind: 'mutation', before: new Date(await now()).toISOString(), batchSize: 100, maxBatches: 1, apply: true, ...extra })
  const waitPast = async deadline => { const delay = deadline - await now() + 30; if (delay > 0) await new Promise(resolve => setTimeout(resolve, delay)) }
  const lock = new pg.Client({ connectionString: migrationURL }), direct = new pg.Client({ connectionString: loginURL })
  await lock.connect(); await direct.connect()
  const waitForBlockers = async (pid, count) => {
    for (let i = 0; i < 100; i++) {
      const blocked = (await owner.query('SELECT count(*)::integer AS n FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid))', [pid])).rows[0].n
      if (blocked >= count) return
      await new Promise(resolve => setTimeout(resolve, 10))
    }
    assert.fail('mutation requests did not enter the expected lock wait')
  }
  try {
    const original = await make('original'), updateKey = createReplayKeyV2(), patch = { body: 'mutation-updated', priority: 3 }
    const concurrent = await Promise.all(Array.from({ length: 6 }, () => update(updateKey, original, patch)))
    for (const response of concurrent) { assert.equal(response.error, null); assert.deepEqual(response.data, concurrent[0].data) }
    const saved = concurrent[0].data[0]
    assert.equal(saved.version, original.version + 1); assert.equal(saved.priority, 3)
    assert.equal(classifyVersionedMutation(concurrent[0], match(saved.id, saved.version)).outcome, 'applied')
    conflict(await update(updateKey, original, { body: 'different' }), 'idempotency_payload_conflict')
    conflict(await update(updateKey, { ...original, version: 2 }, patch), 'idempotency_payload_conflict')
    conflict(await remove(updateKey, original), 'idempotency_payload_conflict')
    const another = await make('another')
    conflict(await update(updateKey, another, patch), 'idempotency_payload_conflict')
    const later = await update(createReplayKeyV2(), saved, { body: 'mutation-later' })
    assert.equal(later.error, null)
    assert.deepEqual((await update(updateKey, original, patch)).data, [saved], 'replay must not reapply a patch after later edits')
    assert.deepEqual((await readUpdate(updateKey, original, patch)).data, [saved])
    const deleteKey = createReplayKeyV2(), deleting = later.data[0]
    const deletions = await Promise.all(Array.from({ length: 6 }, () => remove(deleteKey, deleting)))
    for (const response of deletions) { assert.equal(response.error, null); assert.deepEqual(response.data, [deleting]) }
    assert.deepEqual((await readDelete(deleteKey, deleting)).data, [deleting])
    assert.deepEqual((await readUpdate(updateKey, original, patch)).data, [saved], 'update receipt survives subsequent deletion')
    assert.deepEqual((await update(updateKey, original, patch)).data, [saved])
    assert.equal((await owner.query('SELECT count(*)::integer AS n FROM api.notes WHERE id=$1', [original.id])).rows[0].n, 0, 'replay cannot resurrect a deleted note')

    // Stale, absent and hidden rows disclose the same public conflict. Failures
    // leave no receipt, and a conflicting delete cannot erase a newer edit.
    const stale = await make('stale'), changed = (await update(createReplayKeyV2(), stale, { priority: 5 })).data[0]
    const rejectedKeys = []
    const hidden = await make('hidden', bob)
    for (const row of [stale, { ...stale, id: 2147483647 }, hidden]) {
      for (const action of ['update', 'delete']) {
        const key = createReplayKeyV2(); rejectedKeys.push(key)
        const result = action === 'update' ? await update(key, row, { priority: 0 }) : await remove(key, row)
        conflict(result)
        assert.equal(classifyVersionedMutation(result, () => true).outcome, 'version_conflict')
      }
    }
    assert.equal((await owner.query('SELECT count(*)::int AS n FROM api.note_mutation_receipts_v2 WHERE request_key=ANY($1::uuid[])', [rejectedKeys])).rows[0].n, 0)
    assert.deepEqual((await client.db.from('notes').select().eq('id', stale.id)).data, [changed])
    assert.deepEqual((await readUpdate(updateKey, original, { body: 'private mismatch' }, bob)).data, [])
    const sameKeyBob = await update(updateKey, hidden, { priority: 7 }, bob)
    assert.equal(sameKeyBob.error, null); assert.equal(sameKeyBob.data[0].subject, userB.subject)

    for (const sameKey of [false, true]) {
      const row = await make('race'), key = createReplayKeyV2()
      const raced = await Promise.all([update(key, row, { priority: 9 }), remove(sameKey ? key : createReplayKeyV2(), row)])
      assert.deepEqual(raced.map(r => r.status).sort(), [200, 409])
      conflict(raced.find(r => r.error), sameKey ? 'idempotency_payload_conflict' : 'idempotency_version_conflict')
    }

    for (const action of ['update', 'delete']) {
      const row = await make('lost-' + action), key = createReplayKeyV2(); let calls = 0
      const lossy = makeClient({ url, subject: userA.subject, accessToken: userA.token, fetch: async (...args) => {
        calls++; const response = await fetch(...args); await response.arrayBuffer(); throw new TypeError('lost after commit')
      } })
      const response = action === 'update' ? await update(key, row, { priority: 4 }, lossy) : await remove(key, row, lossy)
      assert.equal(response.status, 0); assert.equal(calls, 1)
      const lookup = action === 'update' ? await readUpdate(key, row, { priority: 4 }) : await readDelete(key, row)
      assert.equal(reconcileMutation(lookup, match(row.id, row.version + (action === 'update' ? 1 : 0))).outcome, 'confirmed')
    }

    // A successful mutation whose transaction has not committed is not visible
    // to a read-only lookup, which must not reserve or replay anything.
    const pending = await make('in-flight'), pendingKey = createReplayKeyV2()
    await direct.query('BEGIN')
    await direct.query("SELECT set_config('request.jwt.claims',$1,true)", [JSON.stringify({ sub: userA.subject })])
    await direct.query('SELECT * FROM api.update_note_once_v2($1,$2,$3,$4::jsonb)', [pendingKey, pending.id, pending.version, '{"priority":1.0}'])
    assert.deepEqual((await readUpdate(pendingKey, pending, { priority: 1 })).data, [])
    await direct.query('COMMIT')
    assert.equal((await readUpdate(pendingKey, pending, { priority: 1 })).data[0].version, 2)
    await direct.query('BEGIN READ ONLY')
    await direct.query("SELECT set_config('request.jwt.claims',$1,true),set_config('gregale.idempotency_rpc','mutation_before_read',true)", [JSON.stringify({ sub: userA.subject })])
    assert.equal((await direct.query('SELECT id FROM api.read_note_update_receipt_v2($1,$2,$3,$4::jsonb)', [updateKey, original.id, original.version, JSON.stringify(patch)])).rows[0].id, original.id)
    assert.equal((await direct.query('SELECT id FROM api.read_note_delete_receipt_v2($1,$2,$3)', [deleteKey, deleting.id, deleting.version])).rows[0].id, deleting.id)
    assert.equal((await direct.query("SELECT current_setting('gregale.idempotency_rpc',true) AS scope")).rows[0].scope, 'mutation_before_read')
    assert.deepEqual((await direct.query('SELECT * FROM api.note_mutation_receipts_v2')).rows, [])
    await direct.query('ROLLBACK')
    assert.deepEqual((await client.db.from('note_mutation_receipts_v2').select().retry(false)).data, [])
    assert.equal((await client.db.from('note_mutation_receipts_v2').insert({ subject: userA.subject, request_key: createReplayKeyV2(), request: {}, response: {} }).retry(false)).error.code, '42501')
    assert.deepEqual((await client.db.from('note_mutation_receipts_v2').update({ response: {} }).eq('request_key', updateKey).select().retry(false)).data, [])
    assert.deepEqual((await client.db.from('note_mutation_receipts_v2').delete().eq('request_key', updateKey).select().retry(false)).data, [])
    await assert.rejects(owner.query('DELETE FROM api.note_mutation_receipts_v2 WHERE request_key=$1', [updateKey]), error => error.message === 'idempotency_replay_window_open')
    await assert.rejects(owner.query("UPDATE api.note_mutation_receipts_v2 SET response='{}' WHERE request_key=$1", [updateKey]), error => error.message === 'idempotency_receipt_immutable')
    assert.equal((await clean()).deleted, '0')
    await assert.rejects(cleanup(loginURL, { contract: 'v2', receiptKind: 'mutation', before: new Date(await now()).toISOString(), apply: true }), /Receipt table owner required/)

    for (const patch of [{}, null, [], { version: 9 }, { subject: userA.subject }, { body: null }, { body: 1 }, { priority: null }, { priority: '3' }, { priority: 1.5 }, { priority: 2147483648 }]) {
      const invalid = await update(createReplayKeyV2(), another, patch)
      assert.equal(invalid.status, 400); assert.equal(invalid.error.message, 'idempotency_mutation_invalid')
    }
    for (const key of [randomUUID(), keyAt(await now() + 360000)]) {
      assert.equal((await remove(key, another)).status, 400)
    }
    for (const action of ['update', 'delete']) {
      const key = keyAt(await now() - 86400000)
      expired(action === 'update' ? await update(key, another, { priority: 0 }) : await remove(key, another))
    }

    // Receipt insertion failure rolls back both the conditional mutation and
    // delete cascades. No key can confirm a rolled-back operation.
    const denied = await make('receipt-denied'), denialKey = createReplayKeyV2()
    const reply = await client.reply(denied.id, 'mutation-cascade')
    assert.equal(reply.error, null)
    await owner.query("CREATE POLICY deny_mutation_receipt ON api.note_mutation_receipts_v2 AS RESTRICTIVE FOR INSERT WITH CHECK (response->>'body' <> 'mutation-receipt-denied')")
    try {
      assert.equal((await update(denialKey, denied, { priority: 1 })).error.code, '42501')
      assert.equal((await remove(createReplayKeyV2(), denied)).error.code, '42501')
      assert.equal((await owner.query('SELECT version FROM api.notes WHERE id=$1', [denied.id])).rows[0].version, 1)
      assert.equal((await owner.query('SELECT count(*)::int AS n FROM api.comments WHERE note_id=$1', [denied.id])).rows[0].n, 1)
      assert.deepEqual((await readUpdate(denialKey, denied, { priority: 1 })).data, [])
    } finally { await owner.query('DROP POLICY deny_mutation_receipt ON api.note_mutation_receipts_v2') }
    assert.equal((await update(denialKey, denied, { priority: 1 })).error, null)

    // Both action locks survive across expiry; cleanup skips their receipts,
    // waiters fail, and purging never enables a subsequent mutation.
    const nearUpdate = await make('near-update'), nearDelete = await make('near-delete')
    const deadline = await now() + 2500, nearKeys = [keyAt(deadline - 86400000), keyAt(deadline - 86400000)]
    const nearResponses = [await update(nearKeys[0], nearUpdate, { priority: 2 }), await remove(nearKeys[1], nearDelete)]
    for (const response of nearResponses) assert.equal(response.error, null)
    await lock.query('BEGIN')
    const pid = (await lock.query('SELECT pg_backend_pid() AS pid')).rows[0].pid
    for (const key of nearKeys) await lock.query("SELECT pg_advisory_xact_lock(hashtextextended(jsonb_build_array('gregale.note_mutation.v2',$1::text,$2::uuid)::text,0))", [userA.subject, key])
    const waiting = [update(nearKeys[0], nearUpdate, { priority: 2 }), remove(nearKeys[1], nearDelete)]
    await waitForBlockers(pid, 2); await waitPast(deadline)
    const skipped = await clean(); assert.equal(skipped.eligible, '2'); assert.equal(skipped.deleted, '0')
    await lock.query('ROLLBACK')
    for (const response of await Promise.all(waiting)) expired(response)
    assert.deepEqual((await readUpdate(nearKeys[0], nearUpdate, { priority: 2 })).data, nearResponses[0].data)
    assert.deepEqual((await readDelete(nearKeys[1], nearDelete)).data, nearResponses[1].data)
    assert.equal((await clean({ apply: false })).eligible, '2')
    await lock.query('BEGIN'); await lock.query('SELECT 1 FROM api.note_mutation_receipts_v2 WHERE request_key=ANY($1::uuid[]) FOR UPDATE', [nearKeys])
    assert.equal((await clean()).deleted, '0'); await lock.query('ROLLBACK')
    const purge = await clean({ batchSize: 1, maxBatches: 2 }); assert.equal(purge.deleted, '2'); assert.equal(purge.remaining, '0')
    expired(await update(nearKeys[0], nearUpdate, { priority: 2 })); expired(await remove(nearKeys[1], nearDelete))
    expired(await readUpdate(nearKeys[0], nearUpdate, { priority: 2 })); expired(await readDelete(nearKeys[1], nearDelete))
    assert.equal((await owner.query('SELECT version FROM api.notes WHERE id=$1', [nearUpdate.id])).rows[0].version, 2)

    // Mutations blocked on business rows also roll back if their window closes.
    const blockedUpdate = await make('blocked-update'), blockedDelete = await make('blocked-delete')
    await lock.query('BEGIN'); await lock.query('SELECT 1 FROM api.notes WHERE id=ANY($1::integer[]) FOR UPDATE', [[blockedUpdate.id, blockedDelete.id]])
    const workDeadline = await now() + 1800
    const working = [update(keyAt(workDeadline - 86400000), blockedUpdate, { priority: 8 }), remove(keyAt(workDeadline - 86400000), blockedDelete)]
    await waitForBlockers(pid, 2); await waitPast(workDeadline); await lock.query('ROLLBACK')
    for (const response of await Promise.all(working)) expired(response)
    const preserved = (await owner.query('SELECT version FROM api.notes WHERE id=ANY($1::integer[])', [[blockedUpdate.id, blockedDelete.id]])).rows
    assert.equal(preserved.length, 2); assert.ok(preserved.every(row => row.version === 1))
  } finally {
    await lock.query('ROLLBACK'); await direct.query('ROLLBACK')
    await lock.end(); await direct.end()
    // Live receipts remain guarded until the disposable database is dropped.
    await owner.query('DELETE FROM api.notes WHERE id=ANY($1::integer[])', [ids])
  }
}
