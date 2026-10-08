import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { realpath } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { notesClient } from '../dist/notes.js'

export async function verifyAuthorization({ url, userA, userB }) {
  assert.ok(url && userA.subject && userB.subject && userA.token && userB.token, 'two_user_configuration_required')
  assert.notEqual(userA.subject, userB.subject, 'subjects_must_differ')
  assert.notEqual(userA.token, userB.token, 'tokens_must_differ')
  const first = notesClient({ url, subject: userA.subject, accessToken: userA.token })
  const second = notesClient({ url, subject: userB.subject, accessToken: userB.token })
  const owned = []
  try {
    for (const [owner, other, subject, otherSubject] of [
      [first, second, userA.subject, userB.subject], [second, first, userB.subject, userA.subject],
    ]) {
      const created = await owner.create(`authorization-${randomUUID()}`)
      assert.equal(created.error, null, 'own_insert_failed')
      assert.equal(created.data.subject, subject, 'own_subject_mismatch')
      const id = created.data.id
      owned.push([owner, id])
      const hidden = await other.db.from('notes').select('id').eq('id', id)
      assert.equal(hidden.error, null, 'cross_subject_read_failed')
      assert.deepEqual(hidden.data, [], 'cross_subject_read_allowed')
      const changed = await other.db.from('notes').update({ body: 'stolen' }).eq('id', id).select('id')
      assert.equal(changed.error, null, 'cross_subject_update_failed')
      assert.deepEqual(changed.data, [], 'cross_subject_update_allowed')
      const removed = await other.db.from('notes').delete().eq('id', id).select('id')
      assert.equal(removed.error, null, 'cross_subject_delete_failed')
      assert.deepEqual(removed.data, [], 'cross_subject_delete_allowed')
      const forged = await other.db.from('notes').insert({ subject, body: 'forbidden' })
      assert.equal(forged.error?.code, '42501', 'cross_subject_insert_allowed')
      const reassigned = await owner.db.from('notes').update({ subject: otherSubject }).eq('id', id)
      assert.equal(reassigned.error?.code, '42501', 'cross_subject_reassignment_allowed')
      const updated = await owner.update(id, { body: 'updated', priority: 1 })
      assert.equal(updated.error, null, 'own_update_failed')
      assert.equal(updated.data.body, 'updated', 'own_update_missing')
      assert.equal(updated.data.priority, 1, 'priority_contract_missing')
      const deleted = await owner.db.from('notes').delete().eq('id', id).select('id')
      assert.equal(deleted.error, null, 'own_delete_failed')
      assert.deepEqual(deleted.data, [{ id }], 'own_delete_missing')
    }
  } finally {
    // Remove only IDs this run received from its own successful inserts.
    const results = await Promise.allSettled(owned.map(([owner, id]) => owner.remove(id)))
    assert.ok(results.every(result => result.status === 'fulfilled' && !result.value.error), 'authorization_cleanup_failed')
  }
}

if (process.argv[1] && await realpath(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    await verifyAuthorization({
      url: process.env.DATA_API_URL,
      userA: { subject: process.env.DATA_API_USER_A_SUBJECT, token: process.env.DATA_API_USER_A_TOKEN },
      userB: { subject: process.env.DATA_API_USER_B_SUBJECT, token: process.env.DATA_API_USER_B_TOKEN },
    })
    console.log('Two-user CRUD and RLS checks passed')
  } catch {
    console.error('Authorization check failed: check two distinct application sessions, the API and its RLS policies')
    process.exitCode = 1
  }
}
