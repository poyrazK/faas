import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { realpath } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { notesClient, noteCursor } from '../dist/notes.js'

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

async function verifyTags(owner, other, subject, otherSubject, id, body, ownedTags, owned, junction) {
  const listTags = junction === 'note_tags' ? owner.listWithTags : owner.listWithFavoriteTags
  const listNotes = junction === 'note_tags' ? owner.listTaggedNotes : owner.listFavoriteTaggedNotes
  const otherPath = junction === 'note_tags' ? 'note_favorite_tags' : 'note_tags'
  const empty = await listTags().eq('id', id).single()
  assert.equal(empty.error, null, 'empty_tags_failed')
  assert.deepEqual(empty.data.tags, [])
  const alternateBefore = await owner.db.from('notes').select(`tags!${otherPath}(id,name)`).eq('id', id).single()
  assert.equal(alternateBefore.error, null, 'alternate_baseline_failed')
  const tags = []
  for (const name of ['first tag', 'second tag']) {
    const created = await owner.db.from('tags').insert({ subject, name }).select('id,name').single()
    assert.equal(created.error, null, 'own_tag_failed')
    tags.push(created.data)
    ownedTags.push([owner, created.data.id])
    const linked = await owner.db.from(junction).insert({ subject, note_id: id, tag_id: created.data.id })
    assert.equal(linked.error, null, 'own_tag_link_failed')
  }
  const duplicate = await owner.db.from(junction).insert({ subject, note_id: id, tag_id: tags[0].id })
  assert.equal(duplicate.error?.code, '23505', 'duplicate_link_allowed')
  const joined = await listTags().eq('id', id).single()
  assert.equal(joined.error, null, 'many_to_many_failed')
  assert.deepEqual(joined.data.tags.sort((a, b) => a.id - b.id), tags)
  const unhinted = await owner.db.from('notes').select('tags(id,name)').eq('id', id).single()
  assert.equal(unhinted.status, 300, 'ambiguous_embed_status_mismatch')
  assert.equal(unhinted.error?.code, 'PGRST201', 'ambiguous_embed_not_rejected')
  assert.equal(unhinted.data, null)
  assert.ok(unhinted.error.hint.includes('note_tags') && unhinted.error.hint.includes('note_favorite_tags'), 'ambiguity_hint_missing_paths')
  const reverseAmbiguous = await owner.db.from('tags').select('notes(id,body)').eq('id', tags[0].id)
  assert.equal(reverseAmbiguous.error?.code, 'PGRST201', 'reverse_ambiguous_embed_not_rejected')
  const wrongPath = await owner.db.from('notes').select(`tags!${otherPath}(id,name)`).eq('id', id).single()
  assert.equal(wrongPath.error, null, 'alternate_path_failed')
  assert.deepEqual(wrongPath.data.tags.sort((a, b) => a.id - b.id), alternateBefore.data.tags.sort((a, b) => a.id - b.id), 'hint_selected_wrong_path')
  const both = await owner.db.from('notes').select(`selected:tags!${junction}(id,name),alternate:tags!${otherPath}(id,name)`).eq('id', id).single()
  assert.equal(both.error, null, 'aliased_paths_failed')
  assert.deepEqual(both.data.selected.sort((a, b) => a.id - b.id), tags)
  assert.deepEqual(both.data.alternate.sort((a, b) => a.id - b.id), alternateBefore.data.tags)
  const invalid = await owner.db.from('notes').select('tags!missing_junction(id)').eq('id', id)
  assert.equal(invalid.error?.code, 'PGRST200', 'invalid_hint_not_rejected')
  const secondNote = await owner.create('shared tag probe')
  assert.equal(secondNote.error, null, 'second_tagged_note_failed')
  owned.push([owner, secondNote.data.id])
  const shared = await owner.db.from(junction).insert({ subject, note_id: secondNote.data.id, tag_id: tags[0].id })
  assert.equal(shared.error, null, 'shared_tag_link_failed')
  for (const tag of tags) {
    const reverse = await listNotes().eq('id', tag.id).single()
    assert.equal(reverse.error, null, 'reverse_many_to_many_failed')
    const expected = [{ id, body }]
    if (tag.id === tags[0].id) expected.push({ id: secondNote.data.id, body: 'shared tag probe' })
    assert.deepEqual(reverse.data.notes.sort((a, b) => a.id - b.id), expected.sort((a, b) => a.id - b.id))
  }
  for (const table of ['tags', junction]) {
    const hidden = await other.db.from(table).select('*').eq('subject', subject)
    assert.equal(hidden.error, null, 'cross_subject_tag_read_failed')
    assert.deepEqual(hidden.data, [], 'cross_subject_tag_read_allowed')
    const updated = await other.db.from(table).update({ subject: otherSubject }).eq('subject', subject).select('*')
    assert.equal(updated.error, null, 'cross_subject_tag_update_failed')
    assert.deepEqual(updated.data, [], 'cross_subject_tag_update_allowed')
    const deleted = await other.db.from(table).delete().eq('subject', subject).select('*')
    assert.equal(deleted.error, null, 'cross_subject_tag_delete_failed')
    assert.deepEqual(deleted.data, [], 'cross_subject_tag_delete_allowed')
  }
  const forgedTag = await other.db.from('tags').insert({ subject, name: 'forged' })
  assert.equal(forgedTag.error?.code, '42501', 'forged_tag_subject_allowed')
  const reassigned = await owner.db.from(junction).update({ subject: otherSubject }).eq('note_id', id)
  assert.equal(reassigned.error?.code, '42501', 'link_subject_reassignment_allowed')
  const forged = await other.db.from(junction).insert({ subject, note_id: id, tag_id: tags[0].id })
  assert.equal(forged.error?.code, '42501', 'forged_link_subject_allowed')
  const foreignNote = await other.db.from(junction).insert({ subject: otherSubject, note_id: id, tag_id: tags[0].id })
  assert.equal(foreignNote.error?.code, '23503', 'foreign_note_attachment_allowed')
  // An owned note still cannot be linked to another user's tag.
  const ownNote = await other.create('tag authorization probe')
  assert.equal(ownNote.error, null, 'probe_note_failed')
  try {
    const foreignTag = await other.db.from(junction).insert({ subject: otherSubject, note_id: ownNote.data.id, tag_id: tags[0].id })
    assert.equal(foreignTag.error?.code, '23503', 'foreign_tag_attachment_allowed')
  } finally {
    const removed = await other.remove(ownNote.data.id)
    assert.equal(removed.error, null, 'probe_cleanup_failed')
  }
}

async function verifyPagination(first, second, userA, userB, owned) {
  const marker = `pagination-${randomUUID()}`
  const groups = []
  for (const [client, user] of [[first, userA], [second, userB]]) {
    const rows = []
    for (let i = 0; i < 5; i++) {
      const created = await client.create(marker, i % 2)
      assert.equal(created.error, null, 'page_fixture_failed')
      owned.push([client, created.data.id])
      rows.push(created.data)
    }
    groups.push([client, user, rows])
  }
  for (const [client, user, rows] of groups) {
    const expected = rows.map(row => row.id).reverse()
    const ids = []
    for (const offset of [0, 2, 4]) {
      const page = await client.page({ offset, size: 2 }).eq('body', marker)
      assert.equal(page.error, null, 'page_read_failed')
      assert.equal(page.count, 5, 'count_leaked_other_user_rows')
      assert.deepEqual(page.data.map(row => row.id), expected.slice(offset, offset + 2), 'page_order_mismatch')
      ids.push(...page.data.map(row => row.id))
    }
    assert.deepEqual(ids, expected, 'page_gaps_or_duplicates')
    const filtered = await client.page({ priority: 1 }).eq('body', marker)
    assert.equal(filtered.error, null, 'filtered_page_failed')
    assert.equal(filtered.count, 2, 'filtered_count_mismatch')
    assert.ok(filtered.data.every(row => row.priority === 1))
    const head = await client.db.from('notes').select('id', { count: 'exact', head: true }).eq('body', marker)
    assert.equal(head.error, null, 'head_count_failed')
    assert.equal(head.count, 5, 'head_count_leaked_rows')
    assert.equal(head.data, null)
    const hidden = await client.page().eq('body', marker).eq('subject', user.subject === userA.subject ? userB.subject : userA.subject)
    assert.equal(hidden.error, null, 'hidden_page_failed')
    assert.deepEqual(hidden.data, [])
    assert.equal(hidden.count, 0, 'hidden_count_leaked_rows')
    const exhausted = await client.page({ offset: 5, size: 2 }).eq('body', marker)
    assert.equal(exhausted.error, null, 'exhausted_page_failed')
    assert.deepEqual(exhausted.data, [])
    assert.equal(exhausted.count, 5)
    const beyond = await client.page({ offset: 6, size: 2 }).eq('body', marker)
    assert.equal(beyond.status, 416, 'out_of_range_status_mismatch')
    assert.equal(beyond.error?.code, 'PGRST103', 'out_of_range_error_mismatch')
  }
}

async function verifyCursors(first, second, userA, userB, owned) {
  const marker = `cursor-${randomUUID()}`
  const groups = []
  for (const [client, user] of [[first, userA], [second, userB]]) {
    const created = await client.db.from('notes').insert(Array.from({ length: 5 }, (_, i) => ({
      subject: user.subject, body: marker, priority: i % 2,
      created_at: i === 0 ? '2026-10-08T12:00:00.123455Z' : '2026-10-08T12:00:00.123456Z',
    }))).select('id,body,priority,created_at')
    assert.equal(created.error, null, 'cursor_fixture_failed')
    for (const row of created.data) owned.push([client, row.id])
    groups.push([client, user, created.data.sort((a, b) => b.id - a.id)])
  }
  for (const [client, user, rows] of groups) {
    const firstPage = await client.cursorPage({ size: 2 }).eq('body', marker)
    assert.equal(firstPage.error, null, 'cursor_page_failed')
    assert.deepEqual(firstPage.data.map(row => row.id), rows.slice(0, 2).map(row => row.id), 'cursor_tie_order_mismatch')
    const cursor = noteCursor(firstPage.data.at(-1))
    assert.ok(cursor.created_at.includes('.123456'), 'cursor_precision_lost')
    const filtered = await client.cursorPage({ priority: 1 }).eq('body', marker)
    assert.equal(filtered.error, null)
    assert.deepEqual(filtered.data.map(row => row.id), rows.filter(row => row.priority === 1).map(row => row.id))
    // Delete the cursor row and an unread row, then insert ahead of the cursor.
    for (const id of [cursor.id, rows[2].id]) {
      const removed = await client.remove(id)
      assert.equal(removed.error, null, 'cursor_delete_failed')
    }
    const inserted = await client.db.from('notes').insert({ subject: user.subject, body: marker, created_at: '2026-10-09T12:00:00Z' }).select('id').single()
    assert.equal(inserted.error, null, 'cursor_concurrent_insert_failed')
    owned.push([client, inserted.data.id])
    const next = await client.cursorPage({ after: cursor, size: 2 }).eq('body', marker)
    assert.equal(next.error, null, 'cursor_continuation_failed')
    assert.deepEqual(next.data.map(row => row.id), rows.slice(3, 5).map(row => row.id), 'cursor_shifted_after_writes')
    const exhausted = await client.cursorPage({ after: noteCursor(next.data.at(-1)), size: 2 }).eq('body', marker)
    assert.equal(exhausted.error, null)
    assert.deepEqual(exhausted.data, [])
    const hidden = await client.cursorPage({ after: cursor }).eq('body', marker).eq('subject', user.subject === userA.subject ? userB.subject : userA.subject)
    assert.equal(hidden.error, null)
    assert.deepEqual(hidden.data, [], 'cursor_leaked_other_user_rows')
  }
}

export async function verifyAuthorization({ url, userA, userB }) {
  assert.ok(url && userA.subject && userB.subject && userA.token && userB.token, 'two_user_configuration_required')
  assert.notEqual(userA.subject, userB.subject, 'subjects_must_differ')
  assert.notEqual(userA.token, userB.token, 'tokens_must_differ')
  const first = notesClient({ url, subject: userA.subject, accessToken: userA.token })
  const second = notesClient({ url, subject: userB.subject, accessToken: userB.token })
  const owned = []
  const orphaned = []
  const ownedTags = []
  try {
    await verifyPagination(first, second, userA, userB, owned)
    await verifyCursors(first, second, userA, userB, owned)
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
      for (const junction of ['note_tags', 'note_favorite_tags']) {
        await verifyTags(owner, other, subject, otherSubject, id, created.data.body, ownedTags, owned, junction)
      }
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
      ...ownedTags.map(([owner, id]) => owner.db.from('tags').delete().eq('id', id)),
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
