import test from 'node:test'
import assert from 'node:assert/strict'
import { createDataClient, dataRequest, reconcileMutation } from '../dist/index.js'

const row = { id: 7, owner: 'alice', tenant: 'team-a', request_key: 'key', total: 42 }
const matches = receipt => receipt.owner === 'alice' && receipt.tenant === 'team-a' && receipt.request_key === 'key' && receipt.total === 42
const response = (data, error = null, status = 200) => ({ data, error, status })

test('receipt reconciliation confirms only one matching operation and preserves selected data', () => {
  assert.deepEqual(reconcileMutation(response([row]), matches), { outcome: 'confirmed', row, status: 200 })
  for (const changed of [{ ...row, owner: 'bob' }, { ...row, tenant: 'team-b' }, { ...row, request_key: 'other' }, { ...row, total: 43 }]) {
    assert.deepEqual(reconcileMutation(response([changed]), matches), { outcome: 'unresolved', reason: 'invalid_receipt', error: null, status: 200 })
  }
})

test('missing, expired, malformed and ambiguous receipts never prove rollback or expose rejected rows', async () => {
  assert.deepEqual(reconcileMutation(response([]), matches), { outcome: 'unresolved', reason: 'not_found', error: null, status: 200 })
  for (const data of [null, {}, [null], ['private'], [[row]], [row, row]]) {
    assert.deepEqual(reconcileMutation(response(data), matches), { outcome: 'unresolved', reason: 'invalid_receipt', error: null, status: 200 })
  }
  const failed = reconcileMutation(response([{ private: 'secret' }]), () => { throw new Error('private predicate failure') })
  assert.doesNotMatch(JSON.stringify(failed), /private|secret/)
  assert.equal(reconcileMutation(response([row]), async () => true).outcome, 'unresolved')
  assert.equal(reconcileMutation(response([row]), async () => { throw new Error('private async predicate') }).outcome, 'unresolved')
  await new Promise(resolve => setImmediate(resolve))
})

test('only the explicit receipt payload conflict contract becomes payload_conflict', () => {
  const conflict = { code: 'PT409', message: 'idempotency_payload_conflict' }
  assert.deepEqual(reconcileMutation(response(null, conflict, 409), matches), { outcome: 'payload_conflict', error: conflict, status: 409 })
  for (const [error, status] of [[{ code: 'PT409', message: 'other_conflict' }, 409], [conflict, 500], [null, 409], [{ code: '42501' }, 403], [{ code: 'token_invalid' }, 401], [{ code: 'request_timeout' }, 0], [{ code: 'data_api_rate_limited' }, 429], [null, NaN]]) {
    let checked = false
    const result = reconcileMutation(response(null, error, status), () => { checked = true; return true })
    assert.equal(result.outcome, 'unresolved'); assert.equal(result.reason, 'request_failed')
    assert.equal(result.error, error); assert.equal(result.status, status); assert.equal(checked, false)
  }
})

test('a completed receipt lookup is classified without polling, writes or retries', async () => {
  const calls = []
  const db = createDataClient({ url: 'https://data.example', accessToken: 'token', fetch: async (url, init) => {
    calls.push({ path: new URL(url).pathname, method: init.method, body: JSON.parse(init.body) })
    return new Response(JSON.stringify([row]))
  } }).schema('api')
  const pending = Object.freeze({ request_key: 'key', order_total: 42 })
  const result = await dataRequest(db.rpc('read_order_receipt', pending), { timeoutMs: 1000 })
  assert.equal(reconcileMutation(result, matches).outcome, 'confirmed')
  assert.equal(reconcileMutation(result, matches).outcome, 'confirmed')
  assert.deepEqual(calls, [{ path: '/rest/v1/rpc/read_order_receipt', method: 'POST', body: pending }])
  let executions = 0
  reconcileMutation({ then() { executions++ } }, matches)
  assert.equal(executions, 0)
})
