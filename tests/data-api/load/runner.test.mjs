import test from 'node:test'
import assert from 'node:assert/strict'
import { bounds, LoadFailure, pinnedEngine } from './contracts.mjs'
import { parallel, verifyLoad } from './runner.mjs'

function fixture() {
  const target = { cleanups: 0, setup: async () => ({ runtime_sha256: 'a'.repeat(64), sdk_sha256: 'b'.repeat(64), harness_sha256: 'c'.repeat(64),
    engine_sha256: pinnedEngine.sha256, engine_version: pinnedEngine.version, node_version: 'v22.20.0', postgres_version_num: 160015, fixture_sql_timeout_ms: 30000, rate_per_second: 2, rate_burst: 8 }),
    request: async () => ({ status: 200, code: null, body: 'private-row', token: 'private-token' }),
    cleanup: async () => { target.cleanups++; return { complete: true, dsn: 'postgres://private-password' } }, control: async () => true }
  return target
}
test('parallel workers respect concurrency, stop assigning after failure, and drain started work', async () => {
  let active = 0, peak = 0, started = 0, finished = 0
  await assert.rejects(parallel(100, 3, async index => {
    active++; started++; peak = Math.max(peak, active)
    await new Promise(setImmediate)
    active--; finished++
    if (index === 0) throw new LoadFailure('rls_violation')
  }), /rls_violation/)
  assert.equal(peak, 3); assert.equal(active, 0); assert.equal(started, finished); assert.equal(started, 3)
})
test('failed scenarios retain safe partial traffic, stop later work and preserve primary failure through cleanup', async () => {
  const target = fixture(), saves = []; let later = false
  target.cleanup = async () => { target.cleanups++; throw new Error('private-cleanup-password') }
  const report = await verifyLoad(target, {}, { save: async value => saves.push(JSON.stringify(value)), steps: [
    ['mixed_load', async ctx => { await ctx.request(0, 'read'); throw new LoadFailure('rls_violation') }],
    ['admission_pressure', async () => { later = true }],
  ] })
  assert.equal(report.result, 'failed'); assert.equal(report.failure.code, 'rls_violation'); assert.equal(report.cleanup.state, 'failed')
  assert.equal(target.cleanups, 1); assert.equal(later, false); assert.equal(report.scenarios[0].traffic.requests, 1)
  assert.ok(saves.some(value => JSON.parse(value).scenarios[0]?.result === 'running'))
  assert.doesNotMatch(JSON.stringify(report) + saves.join(''), /private-|postgres:/)
})
test('unexpected errors and setup failures never publish raw error details, and cleanup still runs', async () => {
  const target = fixture(); target.setup = async () => { throw new Error('postgres://private-password private-body') }
  const report = await verifyLoad(target, {})
  assert.equal(report.failure.code, 'verification_failed'); assert.equal(report.cleanup.state, 'complete'); assert.equal(target.cleanups, 1)
  assert.doesNotMatch(JSON.stringify(report), /private-|postgres:/)
})
test('interruptions abort active requests and produce an interrupted report after cleanup', async () => {
  const target = fixture(), controller = new AbortController(); let entered
  const started = new Promise(resolve => { entered = resolve })
  target.request = async (_user, _op, _args, signal) => { entered(); await new Promise(resolve => signal.addEventListener('abort', resolve, { once: true })); return { status: 0, code: 'request_cancelled' } }
  const pending = verifyLoad(target, {}, { signal: controller.signal, steps: [['mixed_load', ctx => ctx.request(0, 'read')]] })
  await started; controller.abort()
  const report = await pending
  assert.equal(report.result, 'interrupted'); assert.equal(report.failure.code, 'interrupted'); assert.equal(report.cleanup.state, 'complete')
  assert.equal(target.cleanups, 1)
})
test('a scenario that ignores cancellation cannot keep the verifier waiting indefinitely', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  const target = fixture(); let entered
  const ready = new Promise(resolve => { entered = resolve })
  const pending = verifyLoad(target, {}, { steps: [['mixed_load', async () => { entered(); return new Promise(() => {}) }]] })
  await ready; t.mock.timers.tick(bounds.scenarioMs + 1)
  const report = await pending
  assert.equal(report.failure.code, 'scenario_timeout'); assert.equal(report.cleanup.state, 'complete'); assert.equal(target.cleanups, 1)
})
test('cleanup and evidence failures cannot produce a successful result', async () => {
  const target = fixture(); target.cleanup = async () => ({ complete: false })
  const report = await verifyLoad(target, {}, { steps: [['mixed_load', async () => {}]] })
  assert.equal(report.result, 'failed'); assert.equal(report.failure.code, 'cleanup_failed')
  const other = fixture()
  const unwritable = await verifyLoad(other, {}, { save: async () => { throw new Error('private-output-path') } })
  assert.equal(unwritable.result, 'failed'); assert.equal(unwritable.failure.code, 'report_write_failed'); assert.equal(other.cleanups, 1)
})
