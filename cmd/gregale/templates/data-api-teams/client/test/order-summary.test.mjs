import test from 'node:test'
import assert from 'node:assert/strict'
import { teamsClient } from '../dist/orders.js'
import { orderSummaryArgs, parseOrderSummary } from '../dist/order-summary.js'

const bucket = (status, count = '0', total = '0') => ({ status, order_count: count, total_cents: total })
const summary = (count = '2', total = '300') => ({ tenant_id: 'shop-a', order_count: count, total_cents: total,
  by_status: [bucket('draft', count, total), bucket('submitted'), bucket('fulfilled')] })

test('summary preserves counts and cents beyond JavaScript precision and freezes the result', () => {
  const value = summary('5000001', '10737420382483647')
  const parsed = parseOrderSummary(value, 'shop-a')
  assert.deepEqual(parsed, value); assert.equal(parsed.total_cents, '10737420382483647')
  assert.ok(BigInt(parsed.total_cents) > 9007199254740991n && BigInt(parsed.total_cents) % 2n === 1n)
  assert.ok(Object.isFrozen(parsed)); assert.ok(Object.isFrozen(parsed.by_status)); assert.ok(Object.isFrozen(parsed.by_status[0]))
  assert.equal(parseOrderSummary(summary('9007199254740993', '0'), 'shop-a').order_count, '9007199254740993')
  assert.equal(parseOrderSummary(summary('0', '0'), 'shop-a').order_count, '0')
  value.by_status[0].total_cents = '0'; assert.equal(parsed.by_status[0].total_cents, '10737420382483647')
})

test('malformed, foreign, imprecise and inconsistent summaries are rejected', () => {
  const good = summary()
  for (const value of [null, [], {}, { ...good, tenant_id: 'shop-b' }, { ...good, private: 'extra' },
    { ...good, order_count: 2 }, { ...good, total_cents: 300 }, { ...good, order_count: '02' },
    { ...good, total_cents: '3e2' }, { ...good, total_cents: '300\n' }, { ...good, total_cents: '-1' },
    { ...good, order_count: '9223372036854775808' }, { ...good, by_status: new Array(3) },
    { ...good, by_status: [good.by_status[0]] }, { ...good, by_status: [...good.by_status].reverse() },
    { ...good, by_status: [good.by_status[0], bucket('draft'), bucket('fulfilled')] },
    { ...good, by_status: [{ ...good.by_status[0], total_cents: '301' }, ...good.by_status.slice(1)] },
    summary('0', '1'), summary('1', '2147483648')]) assert.throws(() => parseOrderSummary(value, 'shop-a'))
  assert.throws(() => parseOrderSummary(good, 'shop-a', { status: 'submitted' }))
  assert.throws(() => parseOrderSummary(good, 'shop-a', { minimum: 151 }))
  assert.throws(() => parseOrderSummary(good, 'shop-a', { maximum: 149 }))
  assert.equal(parseOrderSummary(good, 'shop-a', { status: 'draft', minimum: 150, maximum: 150 }).order_count, '2')
})

test('summary filters fail before token lookup and transport, and arguments are copied', () => {
  let tokens = 0, calls = 0
  const client = teamsClient({ url: 'https://orders.example', accessToken: () => { tokens++; return 'test' }, fetch: async () => { calls++; return new Response('[]') } }, 'shop-a')
  for (const filters of [null, [], { status: 'paid' }, { minimum: -1 }, { maximum: 2147483648 },
    { minimum: 1.5 }, { minimum: NaN }, { maximum: Infinity }, { minimum: 2, maximum: 1 }, { page: 1 }]) assert.throws(() => client.summary(filters))
  assert.equal(tokens, 0); assert.equal(calls, 0)
  const filters = { minimum: 10, maximum: 20, status: 'draft' }
  const args = orderSummaryArgs('shop-a', filters); filters.minimum = 19
  assert.deepEqual(args, { team_id: 'shop-a', status_filter: 'draft', minimum_total: 10, maximum_total: 20 })
  assert.deepEqual(orderSummaryArgs('shop-a'), { team_id: 'shop-a', status_filter: null, minimum_total: null, maximum_total: null })
})

test('summary binds the selected team, supports cancellation and preserves native errors without retries', async () => {
  const calls = [], controller = new AbortController()
  const client = teamsClient({ url: 'https://orders.example', accessToken: 'test', fetch: async (url, init) => {
    calls.push({ url: String(url), init })
    return new Response(JSON.stringify({ code: '42501', message: 'team_membership_required' }), { status: 403 })
  } }, 'shop-a')
  const result = await client.summary({ status: 'submitted', minimum: 100 }).abortSignal(controller.signal)
  assert.equal(result.status, 403); assert.equal(result.error.code, '42501'); assert.equal(calls.length, 1)
  assert.match(calls[0].url, /\/rpc\/summarize_team_orders$/)
  assert.deepEqual(JSON.parse(calls[0].init.body), { team_id: 'shop-a', status_filter: 'submitted', minimum_total: 100, maximum_total: null })
  assert.equal(calls[0].init.signal, controller.signal)
})
