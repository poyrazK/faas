import test from 'node:test'
import assert from 'node:assert/strict'
import { createDataClient, cursorPage, cursorFrom } from '../dist/index.js'
const order = [{ column: 'body', ascending: false, nullsFirst: true }, { column: 'id', unique: true }]
function client() {
  let url
  const db = createDataClient({ url: 'https://data.example', accessToken: 'jwt', fetch: async input => {
    url = new URL(input); return new Response('[]', { status: 200 })
  } })
  return { db, url: () => url }
}
test('preserves filters and generates composite ordering with quoted cursor values', async () => {
  const c = client()
  const after = cursorFrom({ body: 'a,"\\z', id: 2 }, order)
  assert.ok(Object.isFrozen(after))
  await cursorPage(c.db.from('notes').select('id,body').eq('subject', 'alice'), { order, size: 3, after })
  assert.equal(c.url().searchParams.get('subject'), 'eq.alice')
  assert.equal(c.url().searchParams.get('order'), 'body.desc.nullsfirst,id.asc.nullslast')
  assert.equal(c.url().searchParams.get('limit'), '3')
  assert.equal(c.url().searchParams.get('or'), '(body.lt."a,\\"\\\\z",and(body.eq."a,\\"\\\\z",or(id.gt.2,id.is.null)))')
})
test('null-first and null-last continuation excludes or includes the null partition', async () => {
  for (const nullsFirst of [true, false]) {
    const c = client(), ordering = [{ column: 'body', nullsFirst }, { column: 'id', unique: true }]
    await cursorPage(c.db.from('notes').select(), { order: ordering, size: 2, after: [null, 1] })
    assert.equal(c.url().searchParams.get('or').includes('body.not.is.null'), nullsFirst)
    assert.match(c.url().searchParams.get('or'), /body.is.null/)
  }
})
test('rejects malformed cursors before mutating the query', async () => {
  const c = client(), q = c.db.from('notes').select()
  for (const after of [[], ['x'], ['x', null], ['x', Infinity], ['x', Number.MAX_SAFE_INTEGER + 1], ['bad\n', 1], ['x', {}]]) {
    assert.throws(() => cursorPage(q, { order, size: 2, after }))
  }
  assert.throws(() => cursorPage(q, { order, size: 0 }))
  assert.throws(() => cursorPage(q, { order: [{ column: 'id' }], size: 2 }))
  assert.throws(() => cursorFrom({}, order))
  await q
  assert.equal(c.url().searchParams.get('order'), null)
  assert.equal(c.url().searchParams.get('limit'), null)
  assert.equal(c.url().searchParams.get('or'), null)
})
test('before reverses every sort and null partition and preserves filters', async () => {
  const c = client()
  await cursorPage(c.db.from('notes').select('id,body').eq('subject', 'alice'), { order, size: 2, before: ['x', 3] })
  assert.equal(c.url().searchParams.get('order'), 'body.asc.nullslast,id.desc.nullsfirst')
  assert.equal(c.url().searchParams.get('subject'), 'eq.alice')
  assert.equal(c.url().searchParams.get('or'), '(or(body.gt."x",body.is.null),and(body.eq."x",id.lt.3))')
})
test('conflicting or invalid backward cursors leave query unchanged', async () => {
  const c = client(), q = c.db.from('notes').select()
  assert.throws(() => cursorPage(q, { order, size: 2, before: ['x', 1], after: ['x', 2] }), /either/)
  assert.throws(() => cursorPage(q, { order, size: 2, before: ['x', null] }))
  await q
  assert.equal(c.url().searchParams.get('order'), null)
  assert.equal(c.url().searchParams.get('or'), null)
})
