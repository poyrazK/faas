import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, readFile, writeFile, rm, symlink } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
const runtime = process.env.DATA_API_RUNTIME_DIR
const { validateRequests, requestStore } = await import(runtime ? pathToFileURL(join(runtime, 'dev-requests.mjs')) : '../../cmd/gregale/templates/data-api/dev-requests.mjs')
const request = () => ({ name: 'Bob cannot see Alice', method: 'GET', path: '/rest/v1/notes?subject=eq.alice', identity: 'bob', expect: { status: 200, json: [] } })
const collection = () => ({ version: 1, requests: [request()] })

test('saved requests reject credentials, external paths, invalid assertions and oversized collections', () => {
  assert.deepEqual(validateRequests(collection()), collection())
  const invalid = [
    { ...collection(), token: 'secret' },
    { version: 2, requests: [] },
    { version: 1, requests: [request(), request()] },
    { version: 1, requests: Array.from({ length: 101 }, (_, i) => ({ ...request(), name: `${i}` })) }
  ]
  for (const patch of [{ headers: { Authorization: 'secret' } }, { token: 'secret' }, { path: 'https://foreign.invalid/rest/v1/notes' }, { path: '//foreign.invalid/rest/v1/notes' }, { path: '/rest/v1/../../__gregale/dev/session' }, { path: '/rest/v1/notes#fragment' }, { identity: 'owner' }, { method: 'PUT' }, { body: {} }, { expect: { status: 0 } }, { name: '' }]) invalid.push({ version: 1, requests: [{ ...request(), ...patch }] })
  invalid.push({ version: 1, requests: [{ ...request(), method: 'POST', body: 'x'.repeat(65536) }] })
  for (const value of invalid) assert.throws(() => validateRequests(value))
})

test('request storage persists across sessions and rejects conflicting writes', async t => {
  const root = await mkdtemp(join(tmpdir(), 'gregale-requests-')); t.after(() => rm(root, { recursive: true, force: true }))
  const path = join(root, 'data-api.requests.json'), store = requestStore(path)
  await assert.rejects(store.read(true))
  const initial = await store.read()
  assert.deepEqual(initial.requests, [])
  const [first, second] = await Promise.allSettled([store.save(collection(), initial.revision), store.save({ version: 1, requests: [] }, initial.revision)])
  assert.equal(first.status, 'fulfilled'); assert.equal(second.status, 'rejected'); assert.equal(second.reason.status, 409)
  assert.deepEqual((await requestStore(path).read()).requests, collection().requests)
  const stored = await readFile(path, 'utf8')
  assert.equal(stored.includes('token'), false)
  await store.save({ version: 1, requests: [] }, first.value.revision)
  assert.deepEqual((await store.read()).requests, [])
})

test('invalid and symbolic-link collections are preserved rather than overwritten', async t => {
  const root = await mkdtemp(join(tmpdir(), 'gregale-requests-')); t.after(() => rm(root, { recursive: true, force: true }))
  const path = join(root, 'data-api.requests.json'), store = requestStore(path)
  await writeFile(path, 'invalid JSON')
  await assert.rejects(store.read()); await assert.rejects(store.save(collection(), 'anything'))
  assert.equal(await readFile(path, 'utf8'), 'invalid JSON')
  await rm(path)
  const target = join(root, 'target.json'); await writeFile(target, JSON.stringify(collection())); await symlink(target, path)
  await assert.rejects(store.read()); await assert.rejects(store.save(collection(), 'anything'))
  assert.deepEqual(JSON.parse(await readFile(target, 'utf8')), collection())
})

test('replay compares object keys independently of order and requires exact array shape', async () => {
  const { matchesJSON } = await import(runtime ? pathToFileURL(join(runtime, 'request-replay.mjs')) : '../../cmd/gregale/templates/data-api/request-replay.mjs')
  assert.equal(matchesJSON([{ a: 1, b: { c: null } }], [{ b: { c: null }, a: 1 }]), true)
  for (const [actual, expected] of [[[], [{}]], [[1, 2], [2, 1]], [{ a: 1, b: 2 }, { a: 1 }], [null, {}], [1, '1']]) assert.equal(matchesJSON(actual, expected), false)
})

test('replay bounds responses, refuses redirects and propagates cancellation', async t => {
  const { default: http } = await import('node:http')
  const { replayRequest } = await import(runtime ? pathToFileURL(join(runtime, 'request-replay.mjs')) : '../../cmd/gregale/templates/data-api/request-replay.mjs')
  let redirected = 0
  const server = http.createServer((req, res) => {
    if (req.url.endsWith('redirect')) { res.writeHead(302, { Location: '/rest/v1/target' }); res.end() }
    else if (req.url.endsWith('target')) { redirected++; res.end('[]') }
    else if (req.url.endsWith('large')) res.end('x'.repeat(1048577))
    else { res.writeHead(200, { 'Content-Type': 'application/json' }); res.end('[]') }
  })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  t.after(async () => { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)) })
  const context = { url: `http://127.0.0.1:${server.address().port}`, identities: { bob: { token: 'local-test' } } }
  assert.equal((await replayRequest(request(), context)).passed, true)
  assert.equal((await replayRequest({ ...request(), path: '/rest/v1/redirect' }, context)).passed, false)
  assert.equal(redirected, 0)
  assert.equal((await replayRequest({ ...request(), path: '/rest/v1/large' }, context)).passed, false)
  await assert.rejects(replayRequest(request(), { ...context, signal: AbortSignal.abort() }))
})

test('capture definitions validate JSON Pointers and variable names', () => {
  assert.doesNotThrow(() => validateRequests({ version: 1, requests: [{ ...request(), capture: { note_id: '/0/id', scalar: '', escaped: '/a~1b/~0' } }] }))
  for (const capture of [null, [], { 'bad-name': '/id' }, { constructor: '/id' }, { id: 'id' }, { id: '/bad~2escape' }, { id: 0 }]) assert.throws(() => validateRequests({ version: 1, requests: [{ ...request(), capture }] }))
})

test('response variables preserve JSON types, encode paths and fail safely without stale captures', async t => {
  const { default: http } = await import('node:http')
  const { replayRequest } = await import(runtime ? pathToFileURL(join(runtime, 'request-replay.mjs')) : '../../cmd/gregale/templates/data-api/request-replay.mjs')
  const seen = []
  const server = http.createServer(async (req, res) => {
    let body = ''; for await (const chunk of req) body += chunk
    seen.push({ path: req.url, body: body ? JSON.parse(body) : null })
    res.setHeader('Content-Type', 'application/json')
    res.end(JSON.stringify([{ id: 42, text: 'a&b/c', bool: false, nil: null, 'a/b': { '~': 'escaped' } }]))
  })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  t.after(async () => { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)) })
  const variables = new Map(), context = { url: `http://127.0.0.1:${server.address().port}`, identities: { bob: { token: 'local-test' } }, variables }
  const initial = { ...request(), expect: { status: 200 }, capture: { id: '/0/id', text: '/0/text', bool: '/0/bool', nil: '/0/nil', escaped: '/0/a~1b/~0' } }
  const captured = await replayRequest(initial, context)
  assert.equal(captured.passed, true); assert.deepEqual(captured.captured, ['id', 'text', 'bool', 'nil', 'escaped'])
  assert.equal(JSON.stringify(captured).includes('a&b/c'), false)
  const follow = { ...request(), method: 'POST', path: '/rest/v1/notes?id=eq.{{id}}&text=eq.{{text}}', body: { id: '{{id}}', bool: '{{bool}}', nil: '{{nil}}', nested: ['prefix {{escaped}}'] }, expect: { status: 200, json: [{ id: '{{id}}', text: '{{text}}', bool: '{{bool}}', nil: '{{nil}}', 'a/b': { '~': '{{escaped}}' } }] } }
  assert.equal((await replayRequest(follow, context)).passed, true)
  assert.match(seen[1].path, /text=eq.a%26b%2Fc/)
  assert.deepEqual(seen[1].body, { id: 42, bool: false, nil: null, nested: ['prefix escaped'] })
  assert.equal((await replayRequest({ ...follow, capture: { id: '/0/id' } }, context)).passed, true, 'recapture can reference previous value')
  const missing = await replayRequest({ ...initial, capture: { id: '/0/missing', other: '/0/id' } }, context)
  assert.equal(missing.passed, false); assert.match(missing.error, /Cannot capture id/); assert.equal(variables.has('id'), false); assert.equal(variables.has('other'), false)
  const count = seen.length
  assert.match((await replayRequest(follow, context)).error, /Missing replay variable: id/)
  assert.equal(seen.length, count, 'missing references cannot execute writes')
  assert.equal((await replayRequest({ ...initial, capture: { object: '/0' } }, context)).passed, false)
  assert.equal((await replayRequest({ ...initial, expect: { status: 403 } }, context)).passed, false)
  assert.equal(variables.has('text'), false, 'failed assertion clears declared captures')
  variables.set('literal', '{{not_a_variable}}')
  assert.equal((await replayRequest({ ...request(), method: 'POST', body: { text: 'prefix {{literal}}' }, expect: { status: 200 } }, context)).passed, true)
  assert.deepEqual(seen.at(-1).body, { text: 'prefix {{not_a_variable}}' }, 'captured values are never reinterpreted as templates')
  assert.match((await replayRequest({ ...request(), path: '/rest/v1/notes?id={{bad-name}}' }, context)).error, /Invalid replay variable template/)

})

test('scenario collections preserve legacy files, select names and enforce scoped uniqueness and aggregate limits', async () => {
  const { collectionScenarios } = await import(runtime ? pathToFileURL(join(runtime, 'request-replay.mjs')) : '../../cmd/gregale/templates/data-api/request-replay.mjs')
  const value = { version: 2, scenarios: [{ name: 'notes-rls', requests: [request()] }, { name: 'rpc', requests: [request()] }] }
  assert.deepEqual(validateRequests(value), value)
  assert.deepEqual(collectionScenarios(collection()), [{ name: 'default', requests: collection().requests }])
  assert.deepEqual(collectionScenarios(value, 'rpc'), [value.scenarios[1]])
  assert.throws(() => collectionScenarios(value, 'missing'), /Unknown replay scenario/)
  for (const invalid of [
    { version: 2, requests: [], scenarios: value.scenarios },
    { version: 2, scenarios: [] },
    { version: 2, scenarios: [value.scenarios[0], value.scenarios[0]] },
    { version: 2, scenarios: [{ name: 'bad name', requests: [] }] },
    { version: 2, scenarios: [{ name: 'duplicate', requests: [request(), request()] }] },
    { version: 2, scenarios: Array.from({ length: 101 }, (_, i) => ({ name: `scenario${i}`, requests: [] })) },
    { version: 2, scenarios: ['one', 'two'].map(name => ({ name, requests: Array.from({ length: 51 }, (_, i) => ({ ...request(), name: `${i}` })) })) }
  ]) assert.throws(() => validateRequests(invalid))
})
