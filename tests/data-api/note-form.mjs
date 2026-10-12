import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'

export async function verifyNoteForm({ owner, url, userA, makeClient, createNoteForm }) {
  const keys = [], ids = [], tagIDs = []
  const key = () => { const value = randomUUID(); keys.push(value); return value }
  let calls = 0
  const client = makeClient({ url, subject: userA.subject, accessToken: userA.token, fetch: (input, init) => { calls++; return fetch(input, init) } })
  const form = createNoteForm(client, { createKey: key })
  const record = async note => {
    ids.push(note.id)
    tagIDs.push(...(await owner.query('SELECT tag_id FROM api.note_tags WHERE note_id=$1', [note.id])).rows.map(row => row.tag_id))
  }
  try {
    const invalid = await form.submit({ body: ' ', tags: 'one,,two' })
    assert.equal(invalid.outcome, 'validation_failed')
    assert.equal(calls, 0); assert.equal(keys.length, 0)
    const saved = await form.submit({ body: ' validated form ', tags: ' one, two ' })
    assert.equal(saved.outcome, 'saved'); assert.equal(saved.note.body, 'validated form')
    await record(saved.note)
    assert.equal(calls, 1)
    assert.equal((await owner.query('SELECT subject FROM api.notes WHERE id=$1', [saved.note.id])).rows[0].subject, userA.subject)
    assert.equal((await owner.query('SELECT count(*)::integer AS n FROM api.note_tags WHERE note_id=$1', [saved.note.id])).rows[0].n, 2)
    const payloads = []
    let lose = true
    const recoverable = createNoteForm(makeClient({ url, subject: userA.subject, accessToken: userA.token, fetch: async (input, init) => {
      payloads.push(JSON.parse(init.body))
      const response = await fetch(input, init)
      if (lose) { await response.arrayBuffer(); throw new TypeError('lost') }
      return response
    } }), { createKey: key })
    assert.equal((await recoverable.submit({ body: 'recover form', tags: '' })).uncertain, true)
    assert.equal(payloads.length, 1)
    assert.deepEqual(await recoverable.submit({ body: 'different', tags: '' }), { outcome: 'pending' })
    assert.equal(payloads.length, 1)
    const recoveryKey = recoverable.pending.idempotency_key
    lose = false
    const recovered = await recoverable.reconcile()
    assert.equal(recovered.outcome, 'saved'); await record(recovered.note)
    assert.deepEqual(payloads[0], payloads[1]); assert.equal(payloads.length, 2)
    assert.equal((await owner.query('SELECT count(*)::integer AS n FROM api.notes WHERE body=$1 AND subject=$2', ['recover form', userA.subject])).rows[0].n, 1)
    assert.equal((await owner.query('SELECT count(*)::integer AS n FROM api.note_create_receipts WHERE request_key=$1', [recoveryKey])).rows[0].n, 1)
    assert.equal(recoverable.pending, null)
  } finally {
    await owner.query('DELETE FROM api.note_create_receipts WHERE request_key=ANY($1::uuid[])', [keys])
    await owner.query('DELETE FROM api.notes WHERE id=ANY($1::integer[])', [ids])
    await owner.query('DELETE FROM api.tags WHERE id=ANY($1::integer[])', [tagIDs])
  }
}
