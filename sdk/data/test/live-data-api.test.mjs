import test from 'node:test'
import assert from 'node:assert/strict'
import { LIVE_SUBPROTOCOL, LiveConnection, liveRead, liveSocket, parseDataAPIRow, parseDataAPIValue } from '../dist/live/index.js'
import { createDataClient } from '../dist/index.js'

const settle = () => new Promise(resolve => setTimeout(resolve, 0))

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

test('the package exports the live module and its React hooks', async () => {
  const live = await import('@gregale/data/live')
  assert.equal(live.LiveConnection, LiveConnection)
  assert.equal(typeof live.liveSocket, 'function')
  const react = await import('@gregale/data/live/react')
  assert.equal(typeof react.useLiveRead, 'function')
  assert.equal(typeof react.useLiveQuery, 'function')
})

test('liveSocket opens the realtime v2 subprotocol and sends no credential', () => {
  const opened = []
  class Socket extends FakeSocket {
    constructor(url, protocols) { super(); opened.push([url, protocols]) }
  }
  const url = 'wss://data.shop.example/__gregale/realtime/rt-1'
  const connect = liveSocket(url, Socket)
  assert.ok(connect() instanceof Socket)
  assert.ok(connect() instanceof Socket, 'each reconnect opens a new socket')
  assert.deepEqual(opened, [[url, [LIVE_SUBPROTOCOL]], [url, [LIVE_SUBPROTOCOL]]])
  assert.equal(LIVE_SUBPROTOCOL, 'gregale.realtime.v2')
})

test('live values decode as the Data API returns them in JSON', () => {
  for (const [value, type, expected] of [
    [null, 'int8', null],
    ['42', 'int8', 42],
    ['-7', 'int2', -7],
    ['12.50', 'numeric', 12.5],
    ['NaN', 'numeric', 'NaN'],
    ['Infinity', 'float8', 'Infinity'],
    ['1.5', 'float4', 1.5],
    ['t', 'bool', true],
    ['f', 'bool', false],
    ['{"a":[1,null]}', 'jsonb', { a: [1, null] }],
    ['2026-10-18 10:00:00.123456+00', 'timestamptz', '2026-10-18T10:00:00.123456+00:00'],
    ['2026-10-18 10:00:00+05:30', 'timestamptz', '2026-10-18T10:00:00+05:30'],
    ['2026-10-18 10:00:00-07', 'timestamptz', '2026-10-18T10:00:00-07:00'],
    ['infinity', 'timestamptz', 'infinity'],
    ['2026-10-18 10:00:00', 'timestamp', '2026-10-18T10:00:00'],
    ['2026-10-18', 'date', '2026-10-18'],
    ['open', 'note_state', 'open'],
    ['{1,2,NULL}', '_int8', [1, 2, null]],
    ['{"a b","c\\"d",NULL,"NULL",plain}', '_text', ['a b', 'c"d', null, 'NULL', 'plain']],
    ['{{1,2},{3,4}}', '_int4', [[1, 2], [3, 4]]],
    ['{}', '_text', []],
    ['[0:1]={5,6}', '_int4', [5, 6]],
    ['{"{\\"k\\": 1}"}', '_jsonb', [{ k: 1 }]],
    ['{t,f}', '_bool', [true, false]],
    ['(1,x)', 'address', '(1,x)'],
  ]) {
    assert.deepEqual(parseDataAPIValue(value, type), expected, `${type} ${value}`)
  }
  for (const malformed of ['{1,2', '1,2}', '{"open}', '{1}x']) assert.throws(() => parseDataAPIValue(malformed, '_int4'), SyntaxError, malformed)
  assert.deepEqual(parseDataAPIRow(['7', 'hi', null], [{ name: 'id', type: 'int8' }, { name: 'body', type: 'text' }, { name: 'tags', type: '_text' }]),
    { id: 7, body: 'hi', tags: null })
})

test('liveRead decodes rows like the Data API, so generated row types hold', async () => {
  const data = createDataClient({ url: 'https://db.example.test', accessToken: 'tok' })
  const read = liveRead(data.from('notes').select('id,created_at,tags'))
  assert.equal(read.parse, parseDataAPIRow)
  const sockets = []
  const connection = new LiveConnection({ connect: () => { const socket = new FakeSocket(); sockets.push(socket); return socket } })
  await settle()
  const query = connection.query({ database: 'db', token: () => 'user-token', ...read })
  sockets[0].open(); await settle()
  const subscribe = sockets[0].sent.find(frame => frame.type === 'live.subscribe')
  assert.equal(new URL('https://x/' + subscribe.rest).searchParams.get('select'), 'id,created_at,tags')
  sockets[0].deliver({ type: 'live.rows', subscription: query.id, database: 'db', version: 1, snapshot: '1:1:', expires: Math.floor(Date.now() / 1000) + 600,
    columns: [{ name: 'id', type: 'int8' }, { name: 'created_at', type: 'timestamptz' }, { name: 'tags', type: '_text' }],
    rows: [{ id: 'r1', values: ['9007199254740', '2026-10-18 10:00:00+00', '{a,b}'] }], complete: true })
  assert.deepEqual(query.getSnapshot().rows, [{ id: 9007199254740, created_at: '2026-10-18T10:00:00+00:00', tags: ['a', 'b'] }])
  query.close()
  connection.close()
})
