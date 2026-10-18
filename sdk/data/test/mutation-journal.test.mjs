import test from 'node:test'
import assert from 'node:assert/strict'
import { createMutationJournal, createIndexedDBMutationStore, MutationJournalError, createReplayKeyV2, MUTATION_JOURNAL_LIMITS } from '../dist/index.js'

const context = { apiURL: 'https://data.example', subject: 'alice', tenant: 'alpha', contract: 'test_v2', schemaFingerprint: null }
const operation = () => ({ ...context, key: createReplayKeyV2(), payload: { skus: ['a_widget'], quantities: [2] } })
// An atomic test store: one synchronous critical section per CAS. Real browser
// multi-tab transactions are tested against IndexedDB in native acceptance.
const memory = () => {
  const records = new Map()
  return { records, async get(slot) { return records.get(slot) ?? null }, async compareExchange(slot, expected, next) {
    if ((records.get(slot) ?? null) !== expected) return false
    if (next === null) records.delete(slot); else records.set(slot, next)
    return true
  } }
}
const make = (store, extra = {}) => createMutationJournal({ store, context, parser: { parse: value => {
  if (!value.payload || Object.keys(value.payload).sort().join(',') !== 'quantities,skus' ||
      JSON.stringify(value.payload.skus) !== '["a_widget"]' || JSON.stringify(value.payload.quantities) !== '[2]') throw new Error('private parser sentinel')
  return value
} }, ...extra })
const code = expected => error => { assert.equal(error.code, expected); assert.doesNotMatch(error.message, /private.*sentinel/); return true }
const receipt = entry => ({ key: entry.operation.key, subject: 'alice', value: 2 })
const matches = entry => row => row.key === entry.operation.key && row.subject === 'alice' && row.value === 2
const ok = row => ({ data: [row], error: null, status: 200 })

test('durable preparation copies the full operation; fresh journals restore it without requests or new keys', async () => {
  const store = memory(), journal = make(store), input = operation()
  const entry = await journal.prepare('checkout', input)
  input.payload.quantities[0] = 999
  assert.equal(entry.state, 'prepared'); assert.equal(entry.operation.payload.quantities[0], 2)
  assert.ok(Object.isFrozen(entry) && Object.isFrozen(entry.operation) && Object.isFrozen(entry.operation.payload.quantities))
  const restored = await make(store).load('checkout')
  assert.deepEqual(restored, entry); assert.equal(store.records.size, 1)
  await assert.rejects(journal.prepare('checkout', operation()), code('conflict'))
  assert.equal(await journal.load('empty'), null)
  await assert.rejects(journal.removeConfirmed(entry), code('not_confirmed'))
})

test('dispatch persists uncertainty before invoking send; competing tabs choose one claim', async () => {
  const store = memory(), a = make(store), b = make(store), first = await a.prepare('checkout', operation()), second = await b.load('checkout')
  let calls = 0
  const send = async op => { calls++; assert.equal((await make(store).load('checkout')).state, 'uncertain'); return { key: op.key } }
  const raced = await Promise.allSettled([a.dispatch(first, send), b.dispatch(second, send)])
  assert.equal(calls, 1); assert.equal(raced.filter(result => result.status === 'fulfilled').length, 1)
  code('conflict')(raced.find(result => result.status === 'rejected').reason)
  const current = await a.load('checkout')
  await assert.rejects(a.dispatch(current, send), code('not_prepared')); assert.equal(calls, 1)
})

test('storage failures and lost CAS acknowledgements fail closed before sending', async () => {
  const store = memory(), journal = make(store); let calls = 0
  const entry = await journal.prepare('checkout', operation()), exchange = store.compareExchange
  store.compareExchange = async () => { throw new Error('private storage sentinel') }
  await assert.rejects(journal.dispatch(entry, async () => calls++), code('storage_unavailable'))
  assert.equal(calls, 0); assert.equal((await journal.load('checkout')).state, 'prepared')
  const privateError = new MutationJournalError('storage_full'); privateError.message = 'private storage sentinel'
  store.compareExchange = async () => { throw privateError }
  await assert.rejects(journal.dispatch(entry, async () => calls++), code('storage_full'))
  assert.equal(calls, 0)
  store.compareExchange = async (...args) => { await exchange(...args); throw new Error('private storage sentinel') }
  await assert.rejects(journal.dispatch(entry, async () => calls++), code('storage_unavailable'))
  assert.equal(calls, 0); assert.equal((await journal.load('checkout')).state, 'uncertain')
  store.get = async () => { throw new Error('private read sentinel') }
  await assert.rejects(journal.load('checkout'), code('storage_unavailable'))
})

test('a callback failure preserves its original key and input for explicit receipt recovery after restart', async () => {
  const store = memory(), journal = make(store), entry = await journal.prepare('checkout', operation())
  const error = new Error('response lost after commit')
  await assert.rejects(journal.dispatch(entry, async () => { throw error }), failure => failure === error)
  const restarted = make(store), restored = await restarted.load('checkout'); let reads = 0
  assert.equal(restored.state, 'uncertain'); assert.equal(restored.operation.key, entry.operation.key)
  const result = await restarted.recover(restored, async op => { reads++; assert.deepEqual(op, entry.operation); return ok(receipt(restored)) }, matches(restored))
  assert.equal(reads, 1); assert.equal(result.result.outcome, 'confirmed'); assert.equal(result.persistence, 'saved'); assert.equal(result.entry.state, 'confirmed')
  assert.equal((await make(store).load('checkout')).state, 'confirmed')
  await restarted.removeConfirmed(result.entry); assert.equal(await restarted.load('checkout'), null)
})

test('missing, expired, conflicting and malformed receipts retain unresolved operations without pruning', async () => {
  const store = memory(), journal = make(store), entry = await journal.prepare('checkout', operation())
  const original = store.records.get('checkout')
  const responses = [
    { data: [], error: null, status: 200 },
    { data: null, error: { code: 'PT410', message: 'idempotency_key_expired' }, status: 410 },
    { data: null, error: { code: 'PT409', message: 'idempotency_payload_conflict' }, status: 409 },
    { data: null, error: { code: 'request_timeout' }, status: 0 },
    ok({ ...receipt(entry), key: createReplayKeyV2() }), { data: [receipt(entry), receipt(entry)], error: null, status: 200 },
  ]
  for (const response of responses) {
    const result = await journal.recover(entry, async () => response, matches(entry))
    assert.notEqual(result.result.outcome, 'confirmed'); assert.equal(result.persistence, 'unchanged'); assert.equal(store.records.get('checkout'), original)
  }
  assert.equal((await journal.reconcile(entry, responses[1], matches(entry))).result.reason, 'replay_expired')
  await assert.rejects(journal.removeConfirmed(entry), code('not_confirmed'))
})

test('receipt confirmation is retained even if writing its local marker fails or a competing tab wins', async () => {
  const store = memory(), a = make(store), b = make(store), entry = await a.prepare('checkout', operation()), other = await b.load('checkout')
  const first = await b.reconcile(other, ok(receipt(other)), matches(other))
  const stale = await a.reconcile(entry, ok(receipt(entry)), matches(entry))
  assert.equal(first.persistence, 'saved'); assert.equal(stale.result.outcome, 'confirmed'); assert.equal(stale.persistence, 'conflict')
  assert.equal((await a.load('checkout')).state, 'confirmed')
  await assert.rejects(a.recover(entry, async () => assert.fail('stale lookup must not run'), matches(entry)), code('conflict'))
  await b.removeConfirmed(first.entry)
  const next = await a.prepare('checkout', operation())
  await assert.rejects(b.removeConfirmed(first.entry), code('conflict'))
  assert.equal((await a.load('checkout')).operation.key, next.operation.key, 'stale deletion cannot erase a newly staged operation')
  const beforeExchange = store.compareExchange
  store.compareExchange = async () => { throw new Error('private storage sentinel') }
  const confirmed = await a.reconcile(next, ok(receipt(next)), matches(next))
  assert.equal(confirmed.result.outcome, 'confirmed'); assert.equal(confirmed.persistence, 'storage_failed'); assert.equal(confirmed.entry.state, 'prepared')
  store.compareExchange = async (...args) => { await beforeExchange(...args); throw new Error('private lost marker acknowledgement sentinel') }
  const lostMarker = await a.reconcile(next, ok(receipt(next)), matches(next))
  assert.equal(lostMarker.result.outcome, 'confirmed'); assert.equal(lostMarker.persistence, 'storage_failed')
  assert.equal((await make(store).load('checkout')).state, 'confirmed')
})

test('restore rejects changed context, corruption, unknown formats and lossy parsers without changing storage', async () => {
  const store = memory(), journal = make(store), entry = await journal.prepare('checkout', operation()), before = store.records.get('checkout')
  for (const patch of [{ subject: 'bob' }, { tenant: 'beta' }, { apiURL: 'https://other.example' }, { contract: 'test_v3' }, { schemaFingerprint: 'a'.repeat(64) }]) {
    await assert.rejects(make(store, { context: { ...context, ...patch } }).load('checkout'), code('context_mismatch'))
    assert.equal(store.records.get('checkout'), before)
  }
  for (const raw of ['{broken', ' ' + before, before.replace('"version":1', '"version":2'), before.replace('"quantities":[2]', '"quantities":[3]'), JSON.stringify({ ...JSON.parse(before), token: 'private token sentinel' }), undefined, 7]) {
    store.get = async () => raw
    await assert.rejects(journal.load('checkout'), code('invalid_record'))
  }
  store.get = async () => before
  await assert.rejects(make(store, { parser: { parse: input => ({ ...input, key: createReplayKeyV2() }) } }).load('checkout'), code('invalid_record'))
  await assert.rejects(make(store, { parser: { parse: async () => { throw new Error('private parser sentinel') } } }).load('checkout'), code('invalid_record'))
  await assert.rejects(make(store).dispatch(entry, async () => assert.fail()), code('invalid_entry'))
  assert.equal(store.records.get('checkout'), before)
})

test('only bounded plain JSON is accepted; serialization never invokes getters or toJSON', async () => {
  const store = memory(), journal = make(store); let invoked = 0
  const getter = operation(); Object.defineProperty(getter.payload, 'secret', { enumerable: true, get() { invoked++; return 'private getter sentinel' } })
  const custom = operation(); custom.payload.toJSON = () => { invoked++; return {} }
  const circular = operation(); circular.payload.circular = circular
  const deep = operation(); let part = deep.payload; for (let i = 0; i <= MUTATION_JOURNAL_LIMITS.maxDepth; i++) part = part.next = {}
  for (const value of [getter, custom, circular, deep, { ...operation(), payload: { text: 'x'.repeat(MUTATION_JOURNAL_LIMITS.maxEntryBytes) } }, { ...operation(), extra: undefined }, { ...operation(), extra: new Date() }, { ...operation(), extra: Infinity }, { ...operation(), extra: new Array(2) }]) await assert.rejects(journal.prepare('checkout', value), code('invalid_operation'))
  assert.equal(invoked, 0); assert.equal(store.records.size, 0)
  await assert.rejects(journal.prepare('x'.repeat(MUTATION_JOURNAL_LIMITS.maxSlotBytes + 1), operation()), code('invalid_entry'))
  assert.throws(() => createIndexedDBMutationStore({ databaseName: 'test', maxRecords: MUTATION_JOURNAL_LIMITS.maxRecords + 1 }), code('invalid_entry'))
  const absent = createIndexedDBMutationStore({ databaseName: 'test' }); await assert.rejects(absent.get('checkout'), code('storage_unavailable')); absent.close()
})
