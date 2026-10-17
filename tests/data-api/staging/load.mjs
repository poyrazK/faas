import { createHash } from 'node:crypto'
import { readFile, readdir } from 'node:fs/promises'
import { join } from 'node:path'
import { bounds, check, options, LoadFailure } from '../load/contracts.mjs'
import { parallel, verifyLoad } from '../load/runner.mjs'
import { hostedBounds, hostedProfile } from './load-contracts.mjs'
import { requireValue } from './checks.mjs'

async function sourceHash(directories) {
  const hash = createHash('sha256')
  for (const [label, directory] of directories) {
    hash.update(label)
    for (const name of (await readdir(directory)).filter(name => /\.(mjs|sql|js|ts)$/.test(name)).sort()) {
      hash.update(name); hash.update('\0'); hash.update(await readFile(join(directory, name))); hash.update('\0')
    }
  }
  return hash.digest('hex')
}
function boundedResponse(response, maximum, failed = () => {}) {
  if (!response.body) return response
  const reader = response.body.getReader(); let bytes = 0
  return new Response(new ReadableStream({ async pull(controller) {
    try {
      const next = await reader.read()
      if (next.done) { reader.releaseLock(); controller.close(); return }
      bytes += next.value.length
      if (bytes > maximum) { failed(); await reader.cancel(); controller.error(new TypeError('Fixture response bound')); return }
      controller.enqueue(next.value)
    } catch { failed(); controller.error(new TypeError('Fixture response unavailable')) }
  }, cancel: reason => reader.cancel(reason) }), { status: response.status, headers: response.headers })
}
const good = (result, statuses = [200]) => {
  check(result.attempts === 1 && statuses.includes(result.status), 'unexpected_http_result')
  check(result.owned, 'rls_violation')
}
const denied = result => check(result.attempts === 1 &&
  (result.status === 503 && result.code === 'data_api_overloaded' || result.status === 429 && result.code === 'data_api_rate_limited'), 'unexpected_http_result')

// The parent owns all hosted resources. This adapter only configures that
// acknowledged fixture and drains its own HTTP requests during cleanup.
export class HostedFixture {
  constructor(parent, { app, url, jwt, sdk, sdkDirectory }) {
    Object.assign(this, { parent, app, url, jwt, sdk, sdkDirectory, expected: new Map(), pending: new Set(), stop: new AbortController(), wakes: new Set() })
  }
  subject(user) { return `load-${this.runID}-${user}` }
  async setup(runID, signal) {
    this.runID = runID
    const p = this.parent, resources = p.report.resources
    check(p.config.environment === 'staging' && resources.some(r => r.kind === 'app' && r.id === this.app.id && r.slug === this.app.slug && r.state === 'created') &&
      resources.some(r => r.kind === 'database' && r.id && r.state === 'created') && p.report.checks.includes('staging_account'), 'fixture_setup_failed')
    check(p.appURL(this.app) === this.url, 'fixture_setup_failed')
    const deployment = p.report.deployments.find(r => r.app === this.app.slug)
    check(deployment && p.config.builder_node_ids.includes(deployment.builder_node_id), 'source_identity_invalid')
    const metadata = hostedProfile.metadata({ ...deployment, source_commit: p.config.sourceCommit,
      fingerprint: p.report.syncs.at(-1)?.fingerprint, node_version: process.version,
      sdk_sha256: await sourceHash([['sdk', this.sdkDirectory]]),
      harness_sha256: await sourceHash([['shared', join(p.repoRoot, 'tests/data-api/load')], ['hosted', join(p.repoRoot, 'tests/data-api/staging')]]) })
    this.deployment = metadata.deployment_id; this.fingerprint = metadata.fingerprint
    signal.throwIfAborted()
    await p.exec(['secrets', 'set', '--app', this.app.slug, '--from-stdin'], p.workspace,
      'DATA_API_RATE_LIMIT_PER_SECOND=2\nDATA_API_RATE_LIMIT_BURST=8\n', undefined, signal)
    await this.restart(signal)
    return metadata
  }
  async restart(signal) {
    const receipt = await this.parent.refresh(this.app, signal)
    check(receipt?.status === 'completed' && receipt.ready === true && typeof receipt.wake_id === 'string' && /^[a-zA-Z0-9_-]{1,128}$/.test(receipt.wake_id) && !this.wakes.has(receipt.wake_id), 'engine_restart_failed')
    this.wakes.add(receipt.wake_id)
    await this.parent.contract(this.url, this.jwt(this.subject('contract')), this.fingerprint, signal)
    return receipt
  }
  request(user, operation, args = {}, signal) {
    const work = this.perform(user, operation, args, AbortSignal.any([signal, this.stop.signal]))
    this.pending.add(work); work.catch(() => {})
    work.finally(() => this.pending.delete(work)).catch(() => {})
    return work
  }
  async perform(user, operation, args, signal) {
    signal.throwIfAborted()
    let attempts = 0, exceeded = false
    const transport = async (input, init) => {
      attempts++
      const target = new URL(input)
      check(target.origin === this.url && target.pathname.startsWith('/rest/v1/'), 'unexpected_http_result')
      const response = await this.parent.publicFetch(input, { ...init, signal: AbortSignal.any([signal, init.signal].filter(Boolean)) })
      if (args.loseReply) { await response.body?.cancel(); throw new TypeError('Fixture reply discarded') }
      // A bounded parser failure must never count as an expected transport fault.
      return boundedResponse(response, hostedBounds.responseBytes, () => { exceeded = true })
    }
    const db = this.sdk.createDataClient({ url: this.url, accessToken: () => this.jwt(this.subject(user), this.parent.prefix, 300, args.otherTenant ? 'other-tenant' : 'canary'), fetch: transport }).schema('api')
    let query
    const columns = 'key,subject,tenant,body,invocations'
    switch (operation) {
      case 'read': query = db.from('load_notes').select(columns).order('key'); break
      case 'receipt': query = db.from('load_notes').select(columns).eq('key', args.key); break
      case 'rpc_read': query = db.rpc('load_visible'); break
      case 'rest_write': case 'forbidden_write': query = db.from('load_notes').insert({ key: args.key, subject: this.subject(operation === 'forbidden_write' ? args.otherUser : user), body: 'fixture-value' }).select(columns); break
      case 'write': query = db.rpc('load_write', { note_key: args.key, payload: 'fixture-value' }); break
      case 'pause': query = db.rpc('load_pause'); break
      case 'ping': query = db.rpc('load_ping'); break
      default: throw new LoadFailure('verification_failed')
    }
    const result = await this.sdk.dataRequest(query, { signal, timeoutMs: 30000 })
    check(!exceeded, 'unexpected_http_result')
    const rows = result.data == null ? [] : Array.isArray(result.data) ? result.data : [result.data]
    const owned = rows.every(row => row?.subject === this.subject(user) && row.tenant === (args.otherTenant ? 'other-tenant' : 'canary'))
    const valid = rows.every(row => row.body === 'fixture-value' && row.invocations === 1)
    if (['write', 'rest_write'].includes(operation) && [200, 201].includes(result.status) && !args.loseReply) {
      const expected = this.expected.get(user) ?? new Set(); expected.add(args.key); this.expected.set(user, expected)
    }
    if (operation === 'receipt' && result.status === 200 && owned && valid && rows.length === 1 && rows[0].key === args.key && !args.otherTenant) {
      const expected = this.expected.get(user) ?? new Set(); expected.add(args.key); this.expected.set(user, expected)
    }
    return { status: result.status, code: result.error?.code || (result.status === 0 ? 'no_response' : null), attempts, owned, rows: rows.length,
      keys: rows.map(row => row.key), valid_receipts: valid }
  }
  async verify(signal) {
    let rows = 0
    await parallel(this.expected.size, 2, async index => {
      const [user, expected] = [...this.expected][index], result = await this.request(user, 'rpc_read', {}, signal)
      good(result); check(result.valid_receipts && result.rows === expected.size && result.keys.every(key => expected.has(key)), 'receipt_not_reconciled')
      rows += result.rows
    }, signal)
    return rows
  }
  async control(action, _args, signal) {
    const p = this.parent
    if (action === 'verify') return this.verify(signal)
    if (action === 'restart') {
      const receipt = await this.restart(signal)
      p.report.load_lifecycle ??= []
      p.report.load_lifecycle.push({ kind: 'managed_restart', wake_id: receipt.wake_id, deployment_id: this.deployment })
      return true
    }
    if (action === 'park') {
      const instances = await p.wait(() => p.request(`/v1/apps/${this.app.slug}/instances`, { signal }),
        xs => Array.isArray(xs) && xs.some(x => x.state === 'PARKED' && x.deployment_id === this.deployment), hostedBounds.parkMs, signal)
      this.parked = instances.find(x => x.state === 'PARKED' && x.deployment_id === this.deployment)
      return true
    }
    if (action === 'running') {
      const instances = await p.wait(() => p.request(`/v1/apps/${this.app.slug}/instances`, { signal }),
        xs => Array.isArray(xs) && xs.some(x => x.state === 'RUNNING' && x.deployment_id === this.deployment), hostedBounds.scenarioMs, signal)
      const running = instances.find(x => x.state === 'RUNNING' && x.deployment_id === this.deployment)
      check([running.id, this.parked?.id].every(id => typeof id === 'string' && /^[a-zA-Z0-9_-]{1,128}$/.test(id)), 'engine_restart_failed')
      p.report.load_lifecycle ??= []
      p.report.load_lifecycle.push({ kind: 'park_wake', deployment_id: this.deployment, parked_instance_id: this.parked.id, running_instance_id: running.id })
      return true
    }
    if (action === 'healthy') {
      const response = await p.publicFetch(this.url + '/healthz', { signal })
      const bounded = await boundedResponse(response, 8192), value = await bounded.json()
      check(response.status === 200 && value.ready === true, 'engine_restart_failed'); return true
    }
    throw new LoadFailure('verification_failed')
  }
  async cleanup(signal) {
    this.stop.abort(); signal.throwIfAborted()
    await Promise.allSettled([...this.pending]); return { complete: true }
  }
}

async function mixed(ctx, phase) {
  const { users, requests, concurrency } = ctx.workload
  const userFor = i => `${phase}-${i}`, keyFor = i => `${phase}-${i}`
  await parallel(users, 2, async i => {
    const user = userFor(i)
    good(await ctx.request(user, 'rest_write', { key: `${phase}-rest-seed-${i}` }), [201])
    good(await ctx.request(user, 'write', { key: `${phase}-rpc-seed-${i}` }))
    for (const op of ['read', 'rpc_read']) { const result = await ctx.request(user, op); good(result); check(result.rows === 2 && result.valid_receipts, 'rls_violation') }
  }, ctx.signal)
  let completed = 0
  await parallel(requests, concurrency, async i => {
    const operation = ['read', 'rpc_read', 'rest_write', 'write'][Math.floor(i / users) % 4]
    const result = await ctx.request(userFor(i % users), operation, { key: keyFor(i) })
    if ([429, 503].includes(result.status)) denied(result)
    else { good(result, [200, 201]); completed++ }
  }, ctx.signal)
  check(completed > 0, 'pressure_not_observed')
  await ctx.wait(1000); const rows = await ctx.control('verify')
  // Use separate subjects so isolation probes do not consume the workload budget.
  const a = `${phase}-probe-a`, b = `${phase}-probe-b`, key = `${phase}-probe`
  good(await ctx.request(a, 'write', { key }))
  for (const [user, args] of [[b, { key }], [a, { key, otherTenant: true }]]) {
    const hidden = await ctx.request(user, 'receipt', args); good(hidden); check(hidden.rows === 0, 'rls_violation')
  }
  const forbidden = key + '-forbidden', result = await ctx.request(a, 'forbidden_write', { key: forbidden, otherUser: b })
  check(result.status === 403 && result.code === '42501' && result.attempts === 1, 'rls_violation')
  for (const user of [a, b]) { const absent = await ctx.request(user, 'receipt', { key: forbidden }); good(absent); check(absent.rows === 0, 'rejected_write_executed') }
  ctx.observe('rls_checks', users * 2 + completed + 5); ctx.observe('preserved_rows', rows); ctx.observe('automatic_retries', 0)
}
async function admission(ctx) {
  let rejected = 0, completed = 0
  await parallel(12, 12, async i => {
    const result = await ctx.request(`pressure-${i}`, 'pause')
    if (result.status === 503) { denied(result); rejected++ } else { good(result); completed++ }
  }, ctx.signal)
  check(rejected > 0 && completed > 0, 'pressure_not_observed')
  await ctx.control('healthy')
}
async function rate(ctx) {
  let limited = 0
  await parallel(24, 4, async () => {
    const result = await ctx.request('rate', 'ping')
    if (result.status === 429) { denied(result); limited++ } else good(result)
  }, ctx.signal)
  check(limited > 0, 'rate_limit_not_observed')
  good(await ctx.request('rate-other', 'ping')); await ctx.wait(1000); good(await ctx.request('rate', 'ping'))
}
async function lostReply(ctx) {
  const key = 'lost-reply'
  const result = await ctx.request('lost-reply', 'write', { key, loseReply: true })
  check(result.status === 0 && result.attempts === 1, 'unexpected_http_result')
  const receipt = await ctx.request('lost-reply', 'receipt', { key }); good(receipt)
  check(receipt.rows === 1 && receipt.valid_receipts && receipt.keys[0] === key, 'receipt_not_reconciled')
  const hidden = await ctx.request('lost-reply-other', 'receipt', { key }); good(hidden); check(hidden.rows === 0, 'rls_violation')
  ctx.observe('reconciled_writes', 1); ctx.observe('reply_reconciled_without_replay', true); ctx.observe('automatic_retries', 0)
}
async function restart(ctx) {
  good(await ctx.request('durable', 'write', { key: 'durable' }))
  const at = performance.now(); await ctx.control('restart'); await ctx.control('healthy')
  ctx.observe('preserved_rows', await ctx.control('verify')); ctx.observe('recovery_ms', performance.now() - at)
}
async function park(ctx) {
  await ctx.control('park')
  const at = performance.now(), receipt = await ctx.request('durable', 'receipt', { key: 'durable' }); good(receipt)
  check(receipt.rows === 1 && receipt.valid_receipts, 'receipt_not_reconciled')
  const elapsed = performance.now() - at
  await ctx.control('running'); ctx.observe('recovery_ms', elapsed)
}
export const hostedScenarios = Object.freeze([
  ['mixed_load', ctx => mixed(ctx, 'mixed')], ['admission_pressure', admission], ['rate_pressure', rate],
  ['lost_reply', lostReply], ['managed_restart', restart], ['park_wake', park], ['recovery_load', ctx => mixed(ctx, 'recovery')],
])
export async function qualifyHostedLoad(parent, { app, url, jwt, clientDir }) {
  await parent.check('hosted_load_and_recovery', async () => {
    const sdkDirectory = join(clientDir, 'node_modules/@gregale/data/dist')
    const sdk = await parent.loadModule(join(sdkDirectory, 'index.js'))
    const fixture = new HostedFixture(parent, { app, url, jwt, sdk, sdkDirectory })
    const report = await verifyLoad(fixture, options(parent.config.load), { signal: parent.signal, profile: hostedProfile, steps: hostedScenarios,
      save: async value => { check(Buffer.byteLength(JSON.stringify(value)) <= bounds.reportBytes, 'evidence_limit'); parent.report.load = value; await parent.save() } })
    requireValue(report.result === 'passed', 'hosted_load_failed')
  })
}
