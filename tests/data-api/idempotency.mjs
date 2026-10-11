import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'

export async function verifyIdempotency({ owner, client, url, userA, userB, makeClient }) {
  const keys = [], ids = [], tagIDs = []
  const key = () => { const value = randomUUID(); keys.push(value); return value }
  const record = async result => {
    assert.equal(result.error, null)
    ids.push(result.data[0].id)
    const tags = await owner.query('SELECT tag_id FROM api.note_tags WHERE note_id=$1', [result.data[0].id])
    tagIDs.push(...tags.rows.map(row => row.tag_id))
    return result.data[0]
  }
  const replayKey = key()
  try {
    // Requests compete for the same transaction-scoped lock. The result must
    // be identical, not just the eventual number of rows.
    const results = await Promise.all(Array.from({ length: 6 }, () => client.createWithTagsOnce(replayKey, 'once-concurrent', ['one', 'two'])))
    const original = await record(results[0])
    for (const result of results) { assert.equal(result.error, null); assert.deepEqual(result.data, [original]) }
    assert.equal((await owner.query("SELECT count(*)::int AS n FROM api.notes WHERE body='once-concurrent'")).rows[0].n, 1)
    assert.equal((await owner.query('SELECT count(*)::int AS n FROM api.note_tags WHERE note_id=$1', [original.id])).rows[0].n, 2)
    const receipt = await owner.query('SELECT request, response FROM api.note_create_receipts WHERE subject=$1 AND request_key=$2', [userA.subject, replayKey])
    assert.equal(receipt.rows.length, 1)
    assert.deepEqual(receipt.rows[0].request, { note_body: 'once-concurrent', tag_names: ['one', 'two'] })
    assert.equal(receipt.rows[0].response.id, original.id)
    // Snapshot replay preserves the original result after later edits or delete.
    assert.equal((await client.update(original.id, original.version, { body: 'edited later' })).error, null)
    assert.deepEqual((await client.createWithTagsOnce(replayKey, 'once-concurrent', ['one', 'two'])).data, [original])
    for (const [body, tags] of [['changed payload', ['one', 'two']], ['once-concurrent', ['two', 'one']]]) {
      const conflict = await client.createWithTagsOnce(replayKey, body, tags)
      assert.equal(conflict.status, 409)
      assert.equal(conflict.error.code, 'PT409')
      assert.equal(conflict.error.message, 'idempotency_conflict')
    }
    // The same UUID is an independent operation for another subject.
    const bob = await fetch(`${url}/rest/v1/rpc/create_note_with_tags_once`, { method: 'POST', headers: { Authorization: `Bearer ${userB.token}`, 'Content-Type': 'application/json' }, body: JSON.stringify({ idempotency_key: replayKey, note_body: 'bob-once', tag_names: ['bob'] }) })
    assert.equal(bob.status, 200)
    const bobRows = await bob.json()
    const bobNote = await record({ error: null, data: bobRows })
    assert.equal(bobNote.subject, userB.subject)
    assert.notEqual(bobNote.id, original.id)
    assert.equal((await owner.query('SELECT count(*)::int AS n FROM api.note_create_receipts WHERE request_key=$1', [replayKey])).rows[0].n, 2)
    const racingKey = key()
    const racing = await Promise.all(['winner-a', 'winner-b'].map(body => client.createWithTagsOnce(racingKey, body)))
    assert.deepEqual(racing.map(result => result.status).sort(), [200, 409])
    const winner = racing.find(result => result.error === null)
    await record(winner)
    assert.equal(racing.find(result => result.status === 409).error.code, 'PT409')
    assert.equal((await owner.query("SELECT count(*)::int AS n FROM api.notes WHERE body IN ('winner-a', 'winner-b')")).rows[0].n, 1)
    const lostKey = key()
    let sends = 0
    const lossy = makeClient({ url, subject: userA.subject, accessToken: userA.token, fetch: async (...args) => {
      sends++
      const response = await fetch(...args)
      await response.arrayBuffer()
      // The server committed, but the application never received its result.
      throw new TypeError('simulated_response_loss')
    } })
    const lost = await lossy.createWithTagsOnce(lostKey, 'once-lost-response', ['lost'])
    assert.equal(lost.status, 0)
    assert.equal(sends, 1)
    await record(await client.createWithTagsOnce(lostKey, 'once-lost-response', ['lost']))
    assert.equal((await owner.query("SELECT count(*)::int AS n FROM api.notes WHERE body='once-lost-response'")).rows[0].n, 1)
    const defaultsKey = key()
    const defaults = await client.createWithTagsOnce(defaultsKey, 'once-default')
    await record(defaults)
    assert.deepEqual((await client.createWithTagsOnce(defaultsKey, 'once-default', [])).data, defaults.data)
    const failedKey = key()
    const failed = await client.createWithTagsOnce(failedKey, 'once-rollback', ['partial', null])
    assert.equal(failed.error.code, '23502')
    assert.equal((await owner.query('SELECT count(*)::int AS n FROM api.note_create_receipts WHERE request_key=$1', [failedKey])).rows[0].n, 0)
    assert.equal((await owner.query("SELECT count(*)::int AS n FROM api.notes WHERE body='once-rollback'")).rows[0].n, 0)
    assert.equal((await owner.query("SELECT count(*)::int AS n FROM api.tags WHERE name='partial'")).rows[0].n, 0)
    await record(await client.createWithTagsOnce(failedKey, 'once-rollback', ['corrected']))
    const deniedKey = key()
    await owner.query("CREATE POLICY once_deny_tag ON api.tags AS RESTRICTIVE FOR INSERT WITH CHECK (name <> 'blocked')")
    try {
      const denied = await client.createWithTagsOnce(deniedKey, 'once-rls', ['partial', 'blocked'])
      assert.equal(denied.error.code, '42501')
      assert.equal((await owner.query('SELECT count(*)::int AS n FROM api.note_create_receipts WHERE request_key=$1', [deniedKey])).rows[0].n, 0)
      assert.equal((await owner.query("SELECT count(*)::int AS n FROM api.notes WHERE body='once-rls'")).rows[0].n, 0)
    } finally { await owner.query('DROP POLICY once_deny_tag ON api.tags') }
    await record(await client.createWithTagsOnce(deniedKey, 'once-rls', ['corrected']))
    const receiptFailureKey = key()
    await owner.query("CREATE POLICY once_deny_receipt ON api.note_create_receipts AS RESTRICTIVE FOR INSERT WITH CHECK (response->>'body' <> 'once-receipt-denied')")
    try {
      const rejectedReceipt = await client.createWithTagsOnce(receiptFailureKey, 'once-receipt-denied', ['receipt-tag'])
      assert.equal(rejectedReceipt.error.code, '42501')
      assert.equal((await owner.query("SELECT count(*)::int AS n FROM api.notes WHERE body='once-receipt-denied'")).rows[0].n, 0)
      assert.equal((await owner.query("SELECT count(*)::int AS n FROM api.tags WHERE name='receipt-tag'")).rows[0].n, 0)
      assert.equal((await owner.query('SELECT count(*)::int AS n FROM api.note_create_receipts WHERE request_key=$1', [receiptFailureKey])).rows[0].n, 0)
    } finally { await owner.query('DROP POLICY once_deny_receipt ON api.note_create_receipts') }
    await record(await client.createWithTagsOnce(receiptFailureKey, 'once-receipt-denied', ['receipt-tag']))
    const receipts = await client.db.from('note_create_receipts').select('*', { count: 'exact' }).retry(false)
    assert.equal(receipts.error, null)
    assert.deepEqual(receipts.data, [])
    assert.equal(receipts.count, 0, 'function_scope_leaked_to_next_request')
    const forged = await client.db.from('note_create_receipts').insert({ subject: userA.subject, request_key: key(), request: {}, response: {} }).retry(false)
    assert.equal(forged.error.code, '42501')
    const erased = await client.db.from('note_create_receipts').delete().eq('request_key', replayKey).select().retry(false)
    assert.equal(erased.error, null)
    assert.deepEqual(erased.data, [])
    const changed = await client.db.from('note_create_receipts').update({ response: {} }).eq('request_key', replayKey).select().retry(false)
    assert.equal(changed.error, null)
    assert.deepEqual(changed.data, [])
    const nullKey = await client.db.rpc('create_note_with_tags_once', { idempotency_key: null, note_body: 'invalid' }).retry(false)
    assert.equal(nullKey.status, 400)
    assert.equal(nullKey.error.code, '22023')
    assert.equal((await client.remove(original.id)).error, null)
    assert.deepEqual((await client.createWithTagsOnce(replayKey, 'once-concurrent', ['one', 'two'])).data, [original])
    assert.equal((await owner.query('SELECT count(*)::int AS n FROM api.notes WHERE id=$1', [original.id])).rows[0].n, 0)
  } finally {
    await owner.query('DELETE FROM api.note_create_receipts WHERE request_key=ANY($1::uuid[])', [keys])
    await owner.query('DELETE FROM api.notes WHERE id=ANY($1::int[])', [ids])
    await owner.query('DELETE FROM api.tags WHERE id=ANY($1::int[])', [tagIDs])
  }
}
