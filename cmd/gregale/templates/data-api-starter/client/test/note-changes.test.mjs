import test from 'node:test'
import assert from 'node:assert/strict'
import { notesClient } from '../dist/notes.js'
import { noteChangeCursor, parseNoteSyncCheckpoint, parseNoteChangePage, noteChangeCursorExpired } from '../dist/note-changes.js'

const cursor = sequence => `v1.00000000-0000-4000-8000-000000000000.${'a'.repeat(64)}.${sequence}`
const event = (sequence, operation = 'update') => ({ sequence: String(sequence), operation, note_id: 7, version: 2 })

test('change feed parses opaque cursors and lossless ordered positions', () => {
  assert.equal(parseNoteSyncCheckpoint({ cursor: cursor(0) }), cursor(0))
  const large = '9007199254740993'
  const page = parseNoteChangePage({ next_cursor: cursor(large), has_more: false, changes: [event(large)] }, cursor('9007199254740992'))
  assert.equal(page.changes[0].sequence, large); assert.ok(Object.isFrozen(page)); assert.ok(Object.isFrozen(page.changes[0]))
  assert.equal(parseNoteChangePage({ next_cursor: cursor(2), has_more: true, changes: [event(1), event(2, 'delete')] }, cursor(0)).changes.length, 2)
  assert.equal(parseNoteChangePage({ next_cursor: cursor(0), has_more: false, changes: [] }, cursor(0)).changes.length, 0)
  for (const value of [null, '', cursor('01'), cursor('-1'), cursor('9223372036854775808'), cursor(1).toUpperCase(), cursor(1) + '\n', 'x'.repeat(129)]) assert.throws(() => noteChangeCursor(value))
  assert.throws(() => parseNoteSyncCheckpoint({ cursor: cursor(0), extra: true }))
})

test('malformed, discontinuous and foreign pages cannot advance client state', () => {
  const page = { next_cursor: cursor(2), has_more: false, changes: [event(1), event(2)] }
  const invalid = [null, {}, { ...page, changes: null }, { ...page, changes: new Array(2) }, { ...page, has_more: null },
    { ...page, next_cursor: cursor(1) }, { ...page, next_cursor: cursor(2).replace('a'.repeat(64), 'b'.repeat(64)) },
    { ...page, changes: [event(2), event(1)] }, { ...page, changes: [event(1), event(1)] },
    { ...page, changes: [event(1), event(3)], next_cursor: cursor(3) },
    { ...page, changes: [{ ...event(1), note_id: 0 }, event(2)] },
    { ...page, changes: [{ ...event(1), version: 2147483648 }, event(2)] },
    { ...page, changes: [{ ...event(1), subject: 'foreign' }, event(2)] },
    { next_cursor: cursor(0), has_more: true, changes: [] }]
  for (const value of invalid) assert.throws(() => parseNoteChangePage(value, cursor(0)))
})

test('invalid client feed arguments send no request and cursor expiry stays domain specific', () => {
  let calls = 0
  const client = notesClient({ url: 'https://data.example', subject: 'alice', accessToken: 'test', fetch: async () => { calls++; return new Response('[]') } })
  for (const size of [0, -1, 101, 1.5, Infinity]) { assert.throws(() => client.readChanges(cursor(0), size)); assert.throws(() => client.syncRows({ size })) }
  assert.throws(() => client.readChanges('invalid')); assert.throws(() => client.syncRows({ afterID: -1 })); assert.equal(calls, 0)
  assert.equal(noteChangeCursorExpired({ status: 410, error: { code: 'PT410', message: 'note_change_cursor_expired' } }), true)
  for (const response of [{ status: 400, error: { code: 'PT410', message: 'note_change_cursor_expired' } }, { status: 410, error: { code: 'PT410', message: 'idempotency_key_expired' } }, { status: 410, error: null }]) assert.equal(noteChangeCursorExpired(response), false)
})
