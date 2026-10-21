import test from 'node:test'
import assert from 'node:assert/strict'
import { createDataClient, createCursorPager, CursorPaginationError } from '../dist/index.js'
const key = new Uint8Array(32).fill(7), scope = { identity: 'tenant/alice/permissions-v1', schemaFingerprint: 'a'.repeat(64) }
const order = [{ column: 'created_at' }, { column: 'id', unique: true }]
const rows = [{ id: 1, created_at: '2026-01-01T00:00:00Z' }, { id: 2, created_at: '2026-01-01T00:00:00Z' }, { id: 3, created_at: '2026-01-02T00:00:00Z' }]
function client(response = rows) {
 const requests = []
 const db = createDataClient({ url: 'https://data.example', accessToken: 'jwt', fetch: async input => { requests.push(new URL(input)); return new Response(JSON.stringify(response), { status: 200 }) } }).schema('api')
 return { db, requests, query: () => db.from('orders').select('id,created_at').eq('subject', 'alice') }
}
test('encrypts continuation values and preserves the projection, page size and query filters', async () => {
 const c = client(), pager = await createCursorPager({ key, maxPageSize: 10 })
 const page = await pager.page(c.query(), { order, size: 2, scope })
 assert.equal(page.result.data.length, 2); assert.equal(page.hasMore, true); assert.equal(page.previousCursor, null)
 assert.match(page.nextCursor, /^gc1\.[\w-]+\.[\w-]+$/)
 assert.ok(!atob(page.nextCursor.split('.')[2].replaceAll('-', '+').replaceAll('_', '/')).includes('2026'))
 assert.equal(c.requests[0].searchParams.get('limit'), '3')
 await pager.page(c.query(), { order, size: 2, scope, after: page.nextCursor })
 assert.equal(c.requests[1].searchParams.get('subject'), 'eq.alice'); assert.ok(c.requests[1].searchParams.get('or').includes('id.gt.2'))
})
test('rejects expired, forged and incompatible cursors before sending requests or mutating builders', async () => {
 let now = 1000
 const pager = await createCursorPager({ key, ttlMs: 100, now: () => now }), c = client()
 const { nextCursor } = await pager.page(c.query(), { order, size: 2, scope })
 const variants = [
  { query: c.query(), options: { scope: { ...scope, identity: 'tenant/bob' } } },
  { query: c.query(), options: { scope: { ...scope, schemaFingerprint: 'b'.repeat(64) } } },
  { query: c.db.from('orders').select('id,created_at').eq('subject', 'bob'), options: {} },
  { query: c.db.from('other').select('id,created_at').eq('subject', 'alice'), options: {} },
  { query: c.db.from('orders').select('id,created_at,body').eq('subject', 'alice'), options: {} },
  { query: c.query(), options: { order: [{ column: 'created_at', ascending: false }, { column: 'id', unique: true }] } },
  { query: c.query(), options: { size: 1 } },
 ]
 for (const variant of variants) await assert.rejects(pager.page(variant.query, { order, size: 2, scope, after: nextCursor, ...variant.options }), error => error instanceof CursorPaginationError && error.code === 'incompatible_cursor')
 const parts = nextCursor.split('.'); parts[2] = (parts[2][0] === 'A' ? 'B' : 'A') + parts[2].slice(1)
 await assert.rejects(pager.page(c.query(), { order, size: 2, scope, after: parts.join('.') }), error => error.code === 'invalid_cursor')
 now = 1100
 const query = c.query()
 await assert.rejects(pager.page(query, { order, size: 2, scope, after: nextCursor }), error => error.code === 'expired_cursor')
 assert.equal(query.url.searchParams.get('order'), null); assert.equal(query.url.searchParams.get('limit'), null)
 assert.equal(c.requests.length, 1)
})
test('bounds options, rejects reused/mutation builders, and emits backward continuations', async () => {
 const pager = await createCursorPager({ key, maxPageSize: 2 }), c = client()
 for (const options of [{ size: 0 }, { size: 3 }, { after: 'token', before: 'token' }, { order: [{ column: 'id' }] }]) await assert.rejects(pager.page(c.query(), { order, size: 2, scope, ...options }), error => error.code === 'invalid_options')
 await assert.rejects(pager.page(c.query().limit(2), { order, size: 2, scope }), error => error.code === 'invalid_options')
 await assert.rejects(pager.page(c.db.from('orders').delete().select('id,created_at'), { order, size: 2, scope }), error => error.code === 'invalid_options')
 assert.equal(c.requests.length, 0)
 const forward = await pager.page(c.query(), { order, size: 2, scope })
 const backward = await pager.page(c.query(), { order, size: 2, scope, before: forward.nextCursor })
 assert.equal(backward.direction, 'backward'); assert.ok(backward.previousCursor); assert.ok(backward.nextCursor)
 assert.equal(c.requests[1].searchParams.get('order'), 'created_at.desc.nullsfirst,id.desc.nullsfirst')
 await assert.rejects(createCursorPager({ key: new Uint8Array(16) }), error => error.code === 'invalid_options')
 await assert.rejects(createCursorPager({ key, maxPageSize: 1000 }), error => error.code === 'invalid_options')
  await assert.rejects(createCursorPager({ key, ttlMs: 86400001 }), error => error.code === 'invalid_options')
})
