import { check } from './contracts.mjs'
import { parallel } from './runner.mjs'

function success(result, statuses = [200]) {
  check(!result.client_bound_exceeded && result.attempts === 1 && statuses.includes(result.status), 'unexpected_http_result')
  check(result.owned, 'rls_violation')
}
function rejected(result, codes) {
  check(!result.client_bound_exceeded && result.attempts <= 1 && codes.includes(result.code), 'unexpected_http_result')
}
async function mixed(ctx, prefix) {
  const accepted = [], denied = [], { users, requests, concurrency } = ctx.workload
  const since = await ctx.control('record_mark'), profile = await ctx.control('profile')
  let rls = 0
  await parallel(users, Math.min(concurrency, profile.pool), async user => {
    for (const operation of ['read', 'rpc_read']) { const result = await ctx.request(user, operation); success(result); check(result.rows > 0, 'rls_violation'); rls++ }
  }, ctx.signal)
  await parallel(requests, concurrency, async index => {
    const user = index % users, operation = ['read', 'rpc_read', 'rest_write', 'write'][Math.floor(index / users) % 4]
    const key = `${prefix}-${index}`, write = operation === 'rest_write' || operation === 'write'
    const result = await ctx.request(user, operation, { key })
    if (result.status === 429 || result.status === 503) { rejected(result, ['data_api_rate_limited', 'data_api_overloaded']); if (write) denied.push(key) }
    else { success(result, [200, 201]); rls++; if (write) accepted.push(key) }
  }, ctx.signal)
  check(await ctx.control('verify_writes', { accepted, rejected: denied }), 'rejected_write_executed')
  const state = await ctx.control('state')
  const stats = await ctx.control('statistics', { since }), peak = Math.max(state.connections, stats.peak_sql_connections)
  check(peak <= profile.pool, 'admission_bounds_failed')
  ctx.observe('rls_checks', rls); ctx.observe('peak_sql_connections', peak); ctx.observe('automatic_retries', 0)
}
async function admission(ctx) {
  const profile = await ctx.control('profile'), since = await ctx.control('record_mark')
  await ctx.control('lock')
  const active = Array.from({ length: profile.pool }, (_, i) => ctx.request('pressure', 'write', { key: `pressure-active-${i}` }))
  const state = await ctx.control('wait_lock', { count: profile.pool })
  check(state.connections <= profile.pool, 'admission_bounds_failed')
  const arrivals = await ctx.control('arrival_count'), controller = new AbortController()
  const queued = Array.from({ length: profile.queue - 1 }, (_, i) => ctx.request('pressure', 'write', { key: `pressure-denied-${i}` }))
  const cancelled = ctx.request('pressure', 'write', { key: 'pressure-cancelled' }, controller.signal)
  await ctx.control('wait_arrivals', { count: arrivals + profile.queue })
  const full = await ctx.request('pressure', 'write', { key: 'pressure-full' }); rejected(full, ['data_api_overloaded'])
  controller.abort(); rejected(await cancelled, ['request_cancelled'])
  for (const result of await Promise.all(queued)) rejected(result, ['data_api_overloaded'])
  const live = await ctx.control('live'), health = await ctx.control('health')
  check(live.status === 200 && !health.ready && health.engine === 'reachable', 'pressure_not_observed')
  ctx.observe('unknown_pool_observed', health.pool === 'unknown')
  await ctx.control('unlock')
  for (const result of await Promise.all(active)) success(result)
  await ctx.control('healthy')
  const stats = await ctx.control('statistics', { since })
  check(stats.peak_active <= profile.pool && stats.peak_queued <= profile.queue && stats.queue_full > 0 && stats.queue_timeout > 0 && stats.queue_cancelled > 0, 'admission_bounds_failed')
  const accepted = Array.from({ length: profile.pool }, (_, i) => `pressure-active-${i}`)
  const denied = [...Array.from({ length: profile.queue - 1 }, (_, i) => `pressure-denied-${i}`), 'pressure-cancelled', 'pressure-full']
  check(await ctx.control('verify_writes', { accepted, rejected: denied }), 'rejected_write_executed')
  for (const key of ['peak_active', 'peak_queued', 'queue_full', 'queue_timeout', 'queue_cancelled']) ctx.observe(key, stats[key])
  ctx.observe('peak_sql_connections', state.connections)
}
async function rate(ctx) {
  const profile = await ctx.control('profile'); let limited = 0
  await parallel(profile.burst * 3, 4, async () => {
    const result = await ctx.request('rate', 'ping')
    if (result.status === 429) { rejected(result, ['data_api_rate_limited']); limited++ }
    else success(result)
  }, ctx.signal)
  check(limited > 0, 'rate_limit_not_observed')
  success(await ctx.request('other-user', 'ping'))
  await ctx.wait(1000)
  success(await ctx.request('rate', 'ping'))
  ctx.observe('automatic_retries', 0)
}
async function reconcile(ctx, user, key, { present = false, before } = {}) {
  await ctx.control('settle')
  let receipt = await ctx.request(user, 'receipt', { key }); success(receipt)
  check(receipt.rows <= 1, 'duplicate_write')
  if (present) check(receipt.rows === 1, 'receipt_not_reconciled')
  const replayed = receipt.rows === 0
  if (replayed) success(await ctx.request(user, 'write', { key }))
  receipt = await ctx.request(user, 'receipt', { key }); success(receipt)
  check(receipt.rows === 1 && await ctx.control('count', { key }) === 1, 'receipt_not_reconciled')
  const delta = (await ctx.control('invocations')) - before
  check(delta >= 1 && delta <= (replayed ? 2 : 1), 'duplicate_write')
  ctx.observe('reconciled_writes', 1); ctx.observe('sql_settled', true); ctx.observe('automatic_retries', 0)
  if (present) ctx.observe('reply_reconciled_without_replay', !replayed)
}
async function interruptedWrite(ctx, kind) {
  const before = await ctx.control('invocations'), profile = await ctx.control('profile'), since = await ctx.control('record_mark')
  await ctx.control('lock')
  const controller = new AbortController(), key = `uncertain-${kind}`
  const pending = ctx.request(kind, 'write', { key }, controller.signal)
  const state = await ctx.control('wait_lock', { count: 1 }); check(state.connections <= profile.pool, 'admission_bounds_failed')
  success(await ctx.request('independent-user', 'ping'))
  if (kind === 'disconnect') controller.abort()
  const result = await pending
  if (kind === 'disconnect') rejected(result, ['request_cancelled'])
  else {
    rejected(result, ['request_deadline_exceeded']); check(result.status === 504, 'deadline_not_observed')
    ctx.observe('deadline_observed', true)
  }
  await ctx.control('unlock')
  await reconcile(ctx, kind, key, { before })
  if (kind === 'deadline') check((await ctx.control('statistics', { since })).deadlines > 0, 'deadline_not_observed')
}
async function responseLimits(ctx) {
  const before = await ctx.control('invocations'), since = await ctx.control('record_mark')
  const reads = Array.from({ length: 2 }, (_, i) => ctx.request(`large-reader-${i}`, 'ping'))
  const largeRead = await ctx.request('large', 'large_read')
  check(largeRead.status === 502 || largeRead.status === 0, 'unexpected_http_result')
  check(!largeRead.client_bound_exceeded && largeRead.attempts === 1, 'unexpected_http_result')
  const largeWrite = await ctx.request('large', 'large_write', { key: 'large-receipt' })
  check((largeWrite.status === 502 || largeWrite.status === 0) && !largeWrite.client_bound_exceeded && largeWrite.attempts === 1, 'unexpected_http_result')
  for (const result of await Promise.all(reads)) success(result)
  await reconcile(ctx, 'large', 'large-receipt', { present: true, before })
  const stats = await ctx.control('statistics', { since }); check(stats.response_limits === 2, 'pressure_not_observed')
  ctx.observe('response_limits', stats.response_limits)
}
async function lostReply(ctx) {
  const before = await ctx.control('invocations')
  const result = await ctx.request('lost-reply', 'write', { key: 'lost-receipt', loseReply: true })
  check(result.status === 0 && result.attempts === 1 && result.category === 'no_response', 'unexpected_http_result')
  await reconcile(ctx, 'lost-reply', 'lost-receipt', { present: true, before })
}
async function restart(ctx) {
  const rows = await ctx.control('total'), at = performance.now(), profile = await ctx.control('profile')
  const pending = Array.from({ length: profile.pool }, (_, i) => ctx.request(`restart-${i}`, 'pause'))
  await ctx.control('wait_pause')
  await ctx.control('stop_engine')
  const results = await Promise.all(pending)
  for (const result of results) check(result.attempts <= 1 && [0, 200, 503].includes(result.status), 'engine_restart_failed')
  check(results.some(result => result.status === 0 || result.status === 503), 'engine_restart_failed')
  await ctx.control('start_engine'); await ctx.control('healthy')
  check(await ctx.control('total') === rows, 'engine_restart_failed')
  success(await ctx.request('restart-recovered', 'ping'))
  ctx.observe('recovery_ms', performance.now() - at); ctx.observe('preserved_rows', rows); ctx.observe('cold_restarts', 1)
}
export const scenarios = Object.freeze([
  ['mixed_load', ctx => mixed(ctx, 'mixed')], ['admission_pressure', admission], ['rate_pressure', rate],
  ['deadline', ctx => interruptedWrite(ctx, 'deadline')], ['disconnect', ctx => interruptedWrite(ctx, 'disconnect')],
  ['response_limits', responseLimits], ['lost_reply', lostReply], ['engine_restart', restart], ['recovery_load', ctx => mixed(ctx, 'recovery')],
])
