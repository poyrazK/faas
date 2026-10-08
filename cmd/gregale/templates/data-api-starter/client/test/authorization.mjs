import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { realpath } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { notesClient } from '../dist/notes.js'

async function verifyNoteRelationships(owner, other, subject, otherSubject, id, body, orphaned) {
  const empty = await owner.listWithReplies().eq('id', id).single()
  assert.equal(empty.error, null, 'empty_embed_failed')
  assert.deepEqual(empty.data.comments, [], 'empty_many_embed_mismatch')
  assert.equal(empty.data.note_details, null, 'missing_one_embed_mismatch')
  const replies = []
  for (const text of ['first reply', 'second reply']) {
    const reply = await owner.reply(id, text)
    assert.equal(reply.error, null, 'own_reply_failed')
    replies.push({ id: reply.data.id, body: text })
  }
  const details = await owner.db.from('note_details').insert({ subject, note_id: id, summary: 'own summary' })
  assert.equal(details.error, null, 'own_details_failed')
  const duplicate = await owner.db.from('note_details').insert({ subject, note_id: id, summary: 'duplicate' })
  assert.equal(duplicate.error?.code, '23505', 'one_to_one_not_enforced')
  const joined = await owner.listWithReplies().eq('id', id).single()
  assert.equal(joined.error, null, 'nested_select_failed')
  assert.deepEqual(joined.data.comments.sort((a, b) => a.id - b.id), replies)
  assert.deepEqual(joined.data.note_details, { summary: 'own summary' })
  const parent = await owner.listComments().eq('id', replies[0].id).single()
  assert.equal(parent.error, null, 'parent_select_failed')
  assert.deepEqual(parent.data.notes, { id, body })
  const orphan = await owner.db.from('comments').insert({ subject, note_id: null, body: 'unattached' }).select('id').single()
  assert.equal(orphan.error, null, 'nullable_fk_insert_failed')
  orphaned.push([owner, orphan.data.id])
  const nullable = await owner.listComments().eq('id', orphan.data.id).single()
  assert.equal(nullable.error, null, 'nullable_embed_failed')
  assert.equal(nullable.data.notes, null, 'nullable_embed_mismatch')
  const inner = await owner.db.from('comments').select('id,notes!inner(id)').eq('id', orphan.data.id)
  assert.equal(inner.error, null, 'inner_embed_failed')
  assert.deepEqual(inner.data, [], 'inner_embed_kept_unattached_row')
  for (const table of ['comments', 'note_details']) {
    const hidden = await other.db.from(table).select('note_id').eq('note_id', id)
    assert.equal(hidden.error, null, 'cross_subject_child_read_failed')
    assert.deepEqual(hidden.data, [], 'cross_subject_child_read_allowed')
  }
  const attached = await other.reply(id, 'forbidden')
  assert.equal(attached.error?.code, '23503', 'cross_subject_reply_attachment_allowed')
  const forged = await other.db.from('note_details').insert({ subject, note_id: id, summary: 'forbidden' })
  assert.equal(forged.error?.code, '42501', 'cross_subject_details_insert_allowed')
  const attachedDetails = await other.db.from('note_details').insert({ subject: otherSubject, note_id: id, summary: 'forbidden' })
  assert.equal(attachedDetails.error?.code, '23503', 'cross_subject_details_attachment_allowed')
}

export async function verifyAuthorization({ url, userA, userB }) {
  assert.ok(url && userA.subject && userB.subject && userA.token && userB.token, 'two_user_configuration_required')
  assert.notEqual(userA.subject, userB.subject, 'subjects_must_differ')
  assert.notEqual(userA.token, userB.token, 'tokens_must_differ')
  const first = notesClient({ url, subject: userA.subject, accessToken: userA.token })
  const second = notesClient({ url, subject: userB.subject, accessToken: userB.token })
  const owned = []
  const orphaned = []
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
      await verifyNoteRelationships(owner, other, subject, otherSubject, id, created.data.body, orphaned)
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
    const results = await Promise.allSettled([
      ...owned.map(([owner, id]) => owner.remove(id)),
      ...orphaned.map(([owner, id]) => owner.db.from('comments').delete().eq('id', id)),
    ])
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
    console.log('Two-user CRUD, typed joins and RLS checks passed')
  } catch {
    console.error('Authorization check failed: check two distinct application sessions, the API and its RLS policies')
    process.exitCode = 1
  }
}
