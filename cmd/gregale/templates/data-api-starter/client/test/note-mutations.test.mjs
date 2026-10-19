import test from 'node:test'
import assert from 'node:assert/strict'
import { notesClient } from '../dist/notes.js'
import { classifyVersionedMutation, createReplayKeyV2, reconcileMutation } from '@gregale/data'

test('typed mutation methods bind original action, target, version and payload for recovery', async () => {
  const calls = [], key = createReplayKeyV2(), patch = { body: 'updated', priority: 3 }
  const snapshot = { id: 7, subject: 'alice', body: 'updated', priority: 3, version: 2 }
  const client = notesClient({ url: 'https://data.example', subject: 'alice', accessToken: 'token', fetch: async (url, init) => {
    calls.push({ path: new URL(url).pathname, body: JSON.parse(init.body) })
    return new Response(JSON.stringify([snapshot]))
  } })
  const update = await client.updateOnceV2(key, 7, 1, patch).select('id,subject,body,priority,version')
  assert.equal(classifyVersionedMutation(update, row => row.subject === 'alice' && row.id === 7 && row.version === 2).outcome, 'applied')
  const read = await client.readUpdateReceiptV2(key, 7, 1, patch)
  assert.equal(reconcileMutation(read, row => row.subject === 'alice' && row.id === 7).outcome, 'confirmed')
  await client.removeOnceV2(key, 7, 2)
  await client.readDeleteReceiptV2(key, 7, 2)
  assert.deepEqual(calls.map(call => call.path), ['/rest/v1/rpc/update_note_once_v2', '/rest/v1/rpc/read_note_update_receipt_v2', '/rest/v1/rpc/delete_note_once_v2', '/rest/v1/rpc/read_note_delete_receipt_v2'])
  assert.deepEqual(calls[0].body, { idempotency_key: key, note_id: 7, expected_version: 1, note_patch: patch })
  assert.deepEqual(calls[1].body, calls[0].body)
  assert.deepEqual(calls[2].body, { idempotency_key: key, note_id: 7, expected_version: 2 })
  assert.deepEqual(calls[3].body, calls[2].body)
})
