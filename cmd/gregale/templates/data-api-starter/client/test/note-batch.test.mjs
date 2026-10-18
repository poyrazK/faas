import test from 'node:test'
import assert from 'node:assert/strict'
import { notesClient } from '../dist/notes.js'
import { prepareNoteBatch, noteBatchMatches } from '../dist/note-batch.js'
import { classifyVersionedMutation, reconcileMutation } from '@gregale/data'

const input = () => [{ operation: 'update', note_id: 8, expected_version: 2, patch: { body: 'saved', priority: 7 } }, { operation: 'delete', note_id: 4, expected_version: 1 }]
const receipt = pending => ({ request_key: pending.key, subject: pending.subject, results: pending.operations.map((operation, index) => ({
  index, operation: operation.operation,
  note: { id: operation.note_id, subject: pending.subject, body: 'saved', priority: 7, version: operation.expected_version + (operation.operation === 'update' ? 1 : 0), created_at: '2026-10-17T01:02:03.123456+00:00' },
})) })

test('pending batches clone and freeze the complete operation and restore the same key', () => {
  const operations = input(), pending = prepareNoteBatch('alice', operations)
  operations[0].patch.body = 'later'; operations.reverse()
  assert.equal(pending.operations[0].patch.body, 'saved'); assert.equal(pending.operations[0].note_id, 8)
  assert.equal(prepareNoteBatch('alice', pending.operations, pending.key.toUpperCase()).key, pending.key)
  assert.ok(Object.isFrozen(pending)); assert.ok(Object.isFrozen(pending.operations)); assert.ok(Object.isFrozen(pending.operations[0])); assert.ok(Object.isFrozen(pending.operations[0].patch))
  for (const operations of [[], new Array(1), Array.from({ length: 101 }, (_, index) => ({ operation: 'delete', note_id: index + 1, expected_version: 1 })),
    [input()[0], input()[0]], [null], [{ ...input()[0], expected_version: 0 }], [{ ...input()[0], note_id: 1.5 }],
    [{ ...input()[0], subject: 'bob' }], [{ ...input()[1], patch: null }], [{ ...input()[0], patch: {} }],
    [{ ...input()[0], patch: { version: 4 } }], [{ ...input()[0], patch: { body: null } }], [{ ...input()[0], patch: { priority: 2147483648 } }]]) assert.throws(() => prepareNoteBatch('alice', operations), TypeError)
  assert.throws(() => prepareNoteBatch('', input()), TypeError)
  assert.throws(() => prepareNoteBatch('alice', input(), '00000000-0000-4000-8000-000000000000'), TypeError)
  const hundred = Array.from({ length: 100 }, (_, id) => ({ operation: 'delete', note_id: id + 1, expected_version: 1 }))
  assert.equal(prepareNoteBatch('alice', hundred).operations.length, 100)
})

test('whole-batch validation rejects partial, reordered, foreign and malformed JSON receipts', () => {
  const pending = prepareNoteBatch('alice', input()), saved = receipt(pending), matches = noteBatchMatches(pending)
  assert.equal(matches(saved), true); assert.equal(saved.results[0].note.created_at, '2026-10-17T01:02:03.123456+00:00')
  const mutate = fn => { const value = structuredClone(saved); fn(value); return value }
  const invalid = [null, {}, { ...saved, results: null }, { ...saved, results: new Array(2) },
    mutate(value => value.subject = 'bob'), mutate(value => value.request_key = 'other'),
    mutate(value => value.results.pop()), mutate(value => value.results.reverse()),
    mutate(value => value.results[0].index = 1), mutate(value => value.results[0].operation = 'delete'),
    mutate(value => value.results[0].note.id = 4), mutate(value => value.results[0].note.subject = 'bob'),
    mutate(value => value.results[0].note.version = 2), mutate(value => value.results[1].note.version = 2),
    mutate(value => value.results[0].note.body = 'other'), mutate(value => value.results[0].note.priority = '7'),
    mutate(value => value.results[1].note.created_at = 'invalid')]
  for (const row of invalid) {
    const result = { data: [row], error: null, status: 200 }
    assert.equal(matches(row), false)
    assert.equal(classifyVersionedMutation(result, matches).reason, 'invalid_receipt')
    assert.equal(reconcileMutation(result, matches).reason, 'invalid_receipt')
  }
  assert.equal(reconcileMutation({ data: [saved, saved], error: null, status: 200 }, matches).reason, 'invalid_receipt')
})

test('typed batch calls send the same full payload once and read-only recovery confirms it', async () => {
  const pending = prepareNoteBatch('alice', input()), saved = receipt(pending), calls = []
  const client = notesClient({ url: 'https://data.example', subject: 'alice', accessToken: 'token', fetch: async (url, init) => {
    calls.push({ path: new URL(url).pathname, body: JSON.parse(init.body) })
    return new Response(JSON.stringify([saved]))
  } })
  const response = await client.applyBatchOnceV2(pending.key, pending.operations)
  assert.equal(classifyVersionedMutation(response, noteBatchMatches(pending)).outcome, 'applied')
  const lookup = await client.readBatchReceiptV2(pending.key, pending.operations)
  assert.equal(reconcileMutation(lookup, noteBatchMatches(pending)).outcome, 'confirmed')
  assert.deepEqual(calls.map(call => call.path), ['/rest/v1/rpc/apply_note_batch_once_v2', '/rest/v1/rpc/read_note_batch_receipt_v2'])
  assert.deepEqual(calls[0].body, { idempotency_key: pending.key, operations: pending.operations }); assert.deepEqual(calls[1].body, calls[0].body)
  let lostCalls = 0
  const lossy = notesClient({ url: 'https://data.example', subject: 'alice', accessToken: 'token', fetch: async () => { lostCalls++; throw new TypeError('lost') } })
  const lost = await lossy.applyBatchOnceV2(pending.key, pending.operations)
  assert.equal(lost.status, 0); assert.equal(lostCalls, 1)
})
