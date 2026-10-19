import test from 'node:test'
import assert from 'node:assert/strict'
import { notesClient } from '../dist/notes.js'
import { noteSearchCursor, noteSearchText, parseNoteSearchPage } from '../dist/note-search.js'

const cursor = (rank, id) => `v1.${'a'.repeat(64)}.${rank}.${id}`
const row = (id, rank = 100) => ({ id, rank, body: 'running', priority: 0, created_at: '2026-10-17T12:00:00.123456+00:00', version: 1 })

test('search pages preserve deterministic rank/id ordering and immutable results', () => {
  const page = parseNoteSearchPage({ next_cursor: cursor(100, 2), has_more: true, results: [row(3), row(2)] })
  assert.equal(page.next_cursor, cursor(100, 2)); assert.ok(Object.isFrozen(page)); assert.ok(Object.isFrozen(page.results)); assert.ok(Object.isFrozen(page.results[0]))
  assert.equal(parseNoteSearchPage({ next_cursor: null, has_more: false, results: [row(1)] }, page.next_cursor).results[0].id, 1)
  assert.equal(noteSearchText('café 🐈'), 'café 🐈')
  assert.equal(parseNoteSearchPage({ next_cursor: null, has_more: false, results: [] }).results.length, 0)
})

test('malformed and unordered responses cannot supply a search continuation', () => {
  for (const value of ['', cursor(1, 1) + '\n', cursor('01', 1), cursor(1000001, 1), cursor(1, 2147483648), cursor(1, 1).toUpperCase()]) assert.throws(() => noteSearchCursor(value))
  const valid = { next_cursor: cursor(100, 2), has_more: true, results: [row(3), row(2)] }
  for (const value of [null, {}, { ...valid, extra: true }, { ...valid, results: new Array(2) },
    { ...valid, next_cursor: null }, { ...valid, has_more: false }, { ...valid, results: [] },
    { ...valid, results: [row(2), row(3)] }, { ...valid, results: [row(2), row(2)] },
    { ...valid, next_cursor: cursor(99, 2) }, { ...valid, results: [{ ...row(3), subject: 'foreign' }, row(2)] },
    { ...valid, results: [row(3, NaN), row(2)] }]) assert.throws(() => parseNoteSearchPage(value))
  assert.throws(() => parseNoteSearchPage(valid, cursor(100, 3)))
  assert.throws(() => parseNoteSearchPage(valid, cursor(101, 5).replace('a'.repeat(64), 'b'.repeat(64))))
})

test('search validates byte bounds before HTTP and keeps cancellation on the RPC builder', async () => {
  const calls = []
  const client = notesClient({ url: 'https://data.example', subject: 'alice', accessToken: 'test', fetch: async (url, init) => {
    calls.push({ url: String(url), init }); return new Response('[]', { headers: { 'Content-Type': 'application/json' } })
  } })
  for (const query of ['', ' \t', 'é'.repeat(513), '\u0000', '\ud800']) assert.throws(() => client.search(query))
  for (const size of [0, -1, 101, 1.5, Infinity]) assert.throws(() => client.search('running', { size }))
  assert.throws(() => client.search('running', { after: 'bad' })); assert.equal(calls.length, 0)
  const controller = new AbortController()
  await client.search('running', { after: cursor(100, 2), size: 2 }).abortSignal(controller.signal)
  assert.equal(calls.length, 1); assert.match(calls[0].url, /\/rpc\/search_notes$/)
  assert.deepEqual(JSON.parse(calls[0].init.body), { search_text: 'running', page_size: 2, after_cursor: cursor(100, 2) })
  assert.equal(calls[0].init.signal, controller.signal)
})
