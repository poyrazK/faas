import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createElement } from 'react'
import { renderToString } from 'react-dom/server'
import { sql, toPostgresText, createLiveSealer, LiveSealError, LiveConnection, LiveResult, LiveProtocolError, parseLiveServerFrame, LiveSnapshot, liveRead } from '../dist/live/index.js'
import { sealWithFixedNonceForTest } from '../dist/live/seal.js'
import { useLiveQuery } from '../dist/live/react.js'

const shared = (path) => JSON.parse(readFileSync(new URL(`../../../pkg/livequery/${path}`, import.meta.url), 'utf8'))
const fromB64url = (s) => Uint8Array.from(atob(s.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - (s.length % 4)) % 4)), (c) => c.charCodeAt(0))

test('seals byte for byte as pkg/livequery/seal does (shared known-answer vector)', async () => {
  const v = shared('seal/testdata/vector.json')
  assert.equal(JSON.stringify({ db: v.query.db, sql: v.query.sql, params: v.query.params, iat: v.query.iat, exp: v.query.exp }), v.plaintext)
  assert.equal(await sealWithFixedNonceForTest(v.secret, v.plaintext, fromB64url(v.nonce)), v.token)
  const sealer = await createLiveSealer({ secret: v.secret, database: v.query.db })
  const a = await sealer.seal(sql`SELECT ${1}`, { now: v.query.iat })
  const b = await sealer.seal(sql`SELECT ${1}`, { now: v.query.iat })
  assert.match(a, /^gls1\.0123456789abcdef\.[A-Za-z0-9_-]+$/)
  assert.notEqual(a, b, 'every seal uses a fresh nonce')
})

test('sealing enforces the ADR limits before anything leaves the backend', async () => {
  const sealer = await createLiveSealer({ secret: shared('seal/testdata/vector.json').secret, database: 'db_1' })
  const bad = [
    [sql`SELECT 1`, { ttlSeconds: 301 }],
    [sql`SELECT 1`, { ttlSeconds: 0 }],
    [{ text: '   ', params: [] }, {}],
    [{ text: 'SELECT 1' + ' '.repeat(32 << 10), params: [] }, {}],
    [{ text: 'SELECT $1', params: ['a\u0000b'] }, {}],
    [{ text: 'SELECT $1', params: ['\uD800'] }, {}],
    [{ text: 'SELECT 1', params: Array(257).fill('1') }, {}],
    [{ text: 'SELECT 1', params: ['x'.repeat(16 << 10), 'x'.repeat(16 << 10), 'x'.repeat(16 << 10)] }, {}],
  ]
  for (const [query, options] of bad) await assert.rejects(sealer.seal(query, options), LiveSealError)
  await sealer.seal({ text: 'SELECT 1' + ' '.repeat((32 << 10) - 8), params: [] }, { ttlSeconds: 300 })
  await assert.rejects(createLiveSealer({ secret: 'glsk1_0123456789abcdef_AAAA', database: 'db' }), LiveSealError)
  await assert.rejects(createLiveSealer({ secret: shared('seal/testdata/vector.json').secret, database: '' }), LiveSealError)
})

test('the sql tag sends every value as a PostgreSQL text parameter', () => {
  const when = new Date('2026-10-16T10:00:00.000Z')
  const q = sql`SELECT * FROM t WHERE a = ${42} AND b = ${'x'} AND c = ${null} AND d = ${when} AND e = ${[1, 'a"b', null]} AND f = ${{ k: 1 }} AND g = ${12345678901234567890n} AND h = ${true} AND i = ${new Uint8Array([1, 255])}`
  assert.equal(q.text, 'SELECT * FROM t WHERE a = $1 AND b = $2 AND c = $3 AND d = $4 AND e = $5 AND f = $6 AND g = $7 AND h = $8 AND i = $9')
  assert.deepEqual(q.params, ['42', 'x', null, '2026-10-16T10:00:00.000Z', '{"1","a\\"b",NULL}', '{"k":1}', '12345678901234567890', 'true', '\\x01ff'])
  assert.throws(() => toPostgresText(NaN), TypeError)
  assert.throws(() => toPostgresText(Symbol('s')), TypeError)
})

test('the reference client passes the shared session fixtures', () => {
  for (const session of shared('liveproto/testdata/sessions.json')) {
    const r = new LiveResult()
    session.steps.forEach((step, i) => {
      const at = `${session.name}, step ${i}`
      if (step.action === 'resubscribe') { r.resubscribe(); return }
      let err
      try { r.apply(parseLiveServerFrame(JSON.stringify(step.frame))) } catch (e) { err = e }
      if (step.error) { assert.ok(err instanceof LiveProtocolError, `${at}: expected a protocol error, got ${err}`); return }
      assert.equal(err, undefined, `${at}: ${err}`)
      const e = step.expect
      if (e) {
        assert.equal(r.ready, e.ready, at); assert.equal(r.stale, e.stale ?? false, at); assert.equal(r.closed, e.closed ?? false, at)
        if (e.ready) assert.equal(r.version, e.version, at)
        if (e.expires) assert.equal(r.expires, e.expires, at)
        if (e.rows) assert.deepEqual(r.entries(), e.rows, at)
      }
      for (const [xid, want] of Object.entries(step.sees ?? {})) assert.equal(r.sees(BigInt(xid)), want, `${at}: sees ${xid}`)
    })
  }
})

test('snapshots decide visibility and order as PostgreSQL does', () => {
  const s = LiveSnapshot.parse('100:110:105')
  assert.equal(s.visible(99n), true); assert.equal(s.visible(105n), false); assert.equal(s.visible(109n), true); assert.equal(s.visible(110n), false)
  assert.equal(LiveSnapshot.parse('100:111:').covers(s), true)
  assert.equal(LiveSnapshot.parse('100:109:').covers(s), false)
  for (const bad of ['', '1:2', '3:2:', '1:5:7']) assert.throws(() => LiveSnapshot.parse(bad), LiveProtocolError)
})

class FakeSocket {
  readyState = 0
  sent = []
  listeners = { open: [], message: [], close: [], error: [] }
  addEventListener(type, l) { this.listeners[type].push(l) }
  removeEventListener(type, l) { this.listeners[type] = this.listeners[type].filter((x) => x !== l) }
  send(data) { this.sent.push(JSON.parse(data)) }
  close() { this.readyState = 3; this.emit('close', {}) }
  open() { this.readyState = 1; this.emit('open', {}) }
  emit(type, event) { for (const l of [...this.listeners[type]]) l(event) }
  deliver(frame) { this.emit('message', { data: JSON.stringify(frame) }) }
}

class FakeClock {
  t = 1_800_000_000_000
  timers = []
  now() { return this.t }
  setTimeout(fn, ms) { const h = { at: this.t + ms, fn }; this.timers.push(h); return h }
  clearTimeout(h) { this.timers = this.timers.filter((x) => x !== h) }
  advance(ms) {
    this.t += ms
    for (;;) {
      const due = this.timers.filter((h) => h.at <= this.t).sort((a, b) => a.at - b.at)[0]
      if (!due) return
      this.clearTimeout(due); due.fn()
    }
  }
}

const settle = () => new Promise((resolve) => setImmediate(resolve))
const rows = (sub, version, snapshot, data, extra = {}) => ({
  type: 'live.rows', subscription: sub, database: 'db', version, snapshot, expires: 1_800_000_060,
  columns: [{ name: 'id', type: 'int8' }, { name: 'n', type: 'int4' }, { name: 'meta', type: 'jsonb' }, { name: 'ok', type: 'bool' }],
  rows: data, complete: true, ...extra,
})

test('a live query subscribes, follows changes and resolves awaitTxId by snapshot', async () => {
  const clock = new FakeClock()
  const sockets = []
  const conn = new LiveConnection({ connect: () => { const s = new FakeSocket(); sockets.push(s); return s }, clock })
  await settle()
  const seals = ['s2', 's3']
  const q = conn.query({ sealed: 's1', refresh: async () => seals.shift(), initialRows: [{ id: 'ssr' }] })
  assert.equal(q.getSnapshot().status, 'connecting'); assert.deepEqual(q.getSnapshot().rows, [{ id: 'ssr' }])
  let notified = 0
  q.subscribe(() => notified++)
  sockets[0].open(); await settle()
  assert.deepEqual(sockets[0].sent, [{ type: 'live.subscribe', subscription: q.id, sealed: 's1' }])

  sockets[0].deliver(rows(q.id, 1, '100:104:102', [{ id: 'a', values: ['1', '7', '{"k":1}', 't'] }]))
  let state = q.getSnapshot()
  assert.equal(state.status, 'live')
  assert.deepEqual(state.rows, [{ id: '1', n: 7, meta: { k: 1 }, ok: true }])
  assert.deepEqual(state.keys, ['a'])
  assert.ok(notified >= 1)

  const seen = q.awaitTxId(102)
  let resolved = false; seen.then(() => { resolved = true })
  await q.awaitTxId('101') // already visible
  sockets[0].deliver({ type: 'live.changes', subscription: q.id, base: 1, version: 2, snapshot: '100:106:', xids: ['102'],
    changes: [{ op: 'upsert', id: 'b', values: ['2', null, null, 'f'] }] })
  await settle()
  assert.equal(resolved, true)
  const before = q.getSnapshot()
  assert.equal(before.rows.length, 2)
  // A write that does not change the rows still resolves through progress.
  const later = q.awaitTxId(107n)
  sockets[0].deliver({ type: 'live.progress', database: 'db', snapshot: '106:108:' })
  await later
  assert.equal(q.getSnapshot().rows, before.rows, 'unchanged rows keep their identity')

  // Renewal before expiry: a fifth of the lifetime early (12 s of 60), at most 15 s.
  clock.advance(47_000); await settle()
  assert.equal(sockets[0].sent.filter((f) => f.type === 'live.renew').length, 0)
  clock.advance(2_000); await settle()
  assert.deepEqual(sockets[0].sent.at(-1), { type: 'live.renew', subscription: q.id, sealed: 's2' })
  sockets[0].deliver({ type: 'live.renewed', subscription: q.id, expires: 1_800_000_300 })

  // A reconnect resumes from the version held, with a fresh seal.
  sockets[0].close()
  assert.equal(q.getSnapshot().status, 'connecting')
  clock.advance(60_000); await settle()
  sockets[1].open(); await settle()
  assert.deepEqual(sockets[1].sent, [{ type: 'live.subscribe', subscription: q.id, sealed: 's3', version: 2 }])
  sockets[1].deliver(rows(q.id, 1, '120:120:', [{ id: 'c', values: ['3', '1', 'null', 't'] }]))
  assert.deepEqual(q.getSnapshot().keys, ['c'], 'a restarted server may begin a new version sequence')

  q.close()
  assert.deepEqual(sockets[1].sent.at(-1), { type: 'live.unsubscribe', subscription: q.id })
  conn.close()
})

test('stale, refused and closed subscriptions behave as the contract says', async () => {
  const clock = new FakeClock()
  const socket = new FakeSocket()
  const conn = new LiveConnection({ connect: () => socket, clock })
  await settle()
  let refreshes = 0
  const q = conn.query({ sealed: 's1', refresh: async () => `r${++refreshes}` })
  socket.open(); await settle()
  socket.deliver(rows(q.id, 1, '10:10:', [{ id: 'a', values: ['1', '1', null, 't'] }]))

  socket.deliver({ type: 'live.error', subscription: q.id, code: 'seal_expired', message: 'renew', closed: false })
  await settle()
  assert.equal(q.getSnapshot().status, 'stale')
  assert.deepEqual(socket.sent.at(-1), { type: 'live.renew', subscription: q.id, sealed: 'r1' })
  socket.deliver({ type: 'live.renewed', subscription: q.id, expires: 1_800_000_300 })
  socket.deliver({ type: 'live.progress', database: 'db', snapshot: '12:12:' })
  assert.equal(q.getSnapshot().status, 'live')

  // A frame that breaks the contract: start over, keeping the rows shown.
  const shown = q.getSnapshot().rows
  socket.deliver({ type: 'live.changes', subscription: q.id, base: 9, version: 10, snapshot: '13:13:', xids: [], changes: [] })
  await settle()
  assert.deepEqual(socket.sent.slice(-2), [
    { type: 'live.unsubscribe', subscription: q.id },
    { type: 'live.subscribe', subscription: q.id, sealed: 'r2' },
  ])
  assert.equal(q.getSnapshot().rows, shown)

  const pending = q.awaitTxId(500)
  socket.deliver({ type: 'live.error', subscription: q.id, code: 'schema_changed', message: 'messages was altered', closed: true })
  await assert.rejects(pending, /schema_changed/)
  assert.equal(q.getSnapshot().status, 'closed')
  assert.equal(q.getSnapshot().error.code, 'schema_changed')
  await assert.rejects(q.awaitTxId(1), /closed/)
  conn.close()
})

test('useLiveQuery renders the server rows until the live result arrives', () => {
  function Feed({ connection, sealed }) {
    const { rows, status } = useLiveQuery(connection, sealed, { initialRows: [{ body: 'first' }, { body: 'second' }] })
    return createElement('ul', { 'data-status': status }, rows.map((r) => createElement('li', { key: r.body }, r.body)))
  }
  const html = renderToString(createElement(Feed, { connection: null, sealed: null }))
  assert.equal(html, '<ul data-status="connecting"><li>first</li><li>second</li></ul>')
  const conn = new LiveConnection({ connect: () => new FakeSocket() })
  assert.equal(renderToString(createElement(Feed, { connection: conn, sealed: 'gls1.x.y' })), html)
  conn.close()
})

test('liveRead turns a Data API query into the read a live query sends', async () => {
  const { createDataClient } = await import('../dist/index.js')
  const data = createDataClient({ url: 'https://db.example.test', accessToken: 'tok' })
  const read = liveRead(data.from('messages').select('id, body, author:users(name)').eq('channel_id', 1).order('id', { ascending: false }).limit(5))
  assert.equal(read.schema, undefined)
  const url = new URL('https://x/' + read.read)
  assert.equal(url.pathname, '/messages')
  assert.equal(url.searchParams.get('select'), 'id,body,author:users(name)')
  assert.equal(url.searchParams.get('channel_id'), 'eq.1')
  assert.equal(url.searchParams.get('order'), 'id.desc')
  assert.equal(url.searchParams.get('limit'), '5')
  assert.equal(liveRead(data.schema('api').from('notes').select('id')).schema, 'api')
  assert.throws(() => liveRead(data.from('messages').insert({ id: 1 })), TypeError)
  assert.throws(() => liveRead('messages?select=id'), TypeError)
})

test('an RLS-mode query subscribes with its read and the user token, and renews with a fresh token', async () => {
  const clock = new FakeClock()
  const sockets = []
  const conn = new LiveConnection({ connect: () => { const s = new FakeSocket(); sockets.push(s); return s }, clock })
  await settle()
  const tokens = ['t1', 't2', 't3']
  const q = conn.query({ database: 'db_1', read: 'messages?select=id', schema: 'api', token: async () => tokens.shift() })
  sockets[0].open(); await settle()
  assert.deepEqual(sockets[0].sent, [{ type: 'live.subscribe', subscription: q.id, database: 'db_1', rest: 'messages?select=id', schema: 'api', token: 't1' }])
  sockets[0].deliver(rows(q.id, 1, '100:104:', [{ id: 'a', values: ['1', '7', 'null', 't'] }]))
  assert.equal(q.getSnapshot().status, 'live')
  clock.advance(49_000); await settle()
  assert.deepEqual(sockets[0].sent.at(-1), { type: 'live.renew', subscription: q.id, token: 't2' })
  // A token that lapsed while away: stale, then renewed with a fresh one.
  sockets[0].deliver({ type: 'live.error', subscription: q.id, code: 'seal_expired', message: 'expired', closed: false })
  await settle()
  assert.deepEqual(sockets[0].sent.at(-1), { type: 'live.renew', subscription: q.id, token: 't3' })
  q.close()
  conn.close()
})

test('live queries are not published until launch (ADR-20261016151947 rollout)', async () => {
  const pkg = JSON.parse(readFileSync(new URL('../package.json', import.meta.url), 'utf8'))
  assert.deepEqual(Object.keys(pkg.exports), ['.'], 'no ./live export')
  assert.ok(pkg.files.includes('!dist/live'), 'dist/live stays out of the package')
  assert.equal(pkg.peerDependencies?.react, undefined, 'no React peer for an unpublished hook')
  assert.doesNotMatch(readFileSync(new URL('../README.md', import.meta.url), 'utf8'), /live quer/i)
  const root = await import('../dist/index.js')
  assert.equal(Object.keys(root).some((k) => /live/i.test(k)), false, 'the root entry exports nothing live')
})
