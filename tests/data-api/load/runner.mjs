import { randomBytes } from 'node:crypto'
import { setTimeout as delay } from 'node:timers/promises'
import { bounds, check, failureCode, LoadFailure, options, phaseNames, rounded, safeMetadata, Samples } from './contracts.mjs'
import { scenarios } from './scenarios.mjs'

const numericObservations = new Set(['rls_checks', 'peak_active', 'peak_queued', 'peak_sql_connections', 'queue_full', 'queue_timeout', 'queue_cancelled', 'reconciled_writes', 'automatic_retries', 'response_limits', 'recovery_ms', 'preserved_rows', 'cold_restarts'])
const booleanObservations = new Set(['deadline_observed', 'unknown_pool_observed', 'sql_settled', 'reply_reconciled_without_replay'])

export async function parallel(count, concurrency, action, signal) {
  let next = 0, error
  await Promise.all(Array.from({ length: Math.min(count, concurrency) }, async () => {
    while (next < count && !signal?.aborted && !error) {
      const index = next++
      try { await action(index) } catch (failure) { error ??= failure }
    }
  }))
  if (error) throw error
  if (signal?.aborted) throw new LoadFailure('interrupted')
}

async function scoped(action, parent, timeout, code) {
  const controller = new AbortController()
  const signal = parent ? AbortSignal.any([parent, controller.signal]) : controller.signal
  let timer, cancel
  const expired = new Promise((_, reject) => {
    cancel = () => reject(new LoadFailure(parent?.aborted ? 'interrupted' : code))
    signal.addEventListener('abort', cancel, { once: true })
    if (signal.aborted) cancel()
    timer = setTimeout(() => controller.abort(), timeout)
  })
  try { return await Promise.race([Promise.resolve().then(() => action(signal)), expired]) }
  finally { clearTimeout(timer); signal.removeEventListener('abort', cancel); controller.abort() }
}

const localProfile = { scope: 'local-data-api-load', qualification: 'local_fixture_only', runMs: bounds.runMs,
  scenarioMs: () => bounds.scenarioMs, phaseNames, metadata: safeMetadata }

export async function verifyLoad(adapter, rawOptions, { signal, save = async () => {}, steps = scenarios, profile = localProfile } = {}) {
  const workload = options(rawOptions), started = performance.now(), run = new AbortController()
  const runSignal = signal ? AbortSignal.any([run.signal, signal]) : run.signal
  const timer = setTimeout(() => run.abort(), profile.runMs)
  const report = { schema_version: 1, scope: profile.scope, qualification: profile.qualification, run_id: randomBytes(12).toString('hex'),
    started_at: new Date().toISOString(), result: 'running', workload, sources: null, scenarios: [], cleanup: { state: 'pending' } }
  if (profile.coverage) { report.coverage = structuredClone(profile.coverage); report.cleanup_scope = 'client_requests_only'; report.resource_cleanup_owner = 'parent_staging_canary' }
  let primary
  const checkpoint = () => save(report)
  try {
    await checkpoint()
    const metadata = await scoped(s => adapter.setup(report.run_id, s), runSignal, bounds.scenarioMs, 'scenario_timeout')
    report.sources = profile.metadata(metadata)
    await checkpoint()
    for (const [name, step] of steps) {
      check(profile.phaseNames.includes(name))
      const phase = { name, result: 'running', observations: {}, duration_ms: 0, traffic: null }, samples = new Samples(), since = performance.now()
      report.scenarios.push(phase); await checkpoint()
      try {
        await scoped(async phaseSignal => {
          const ctx = { workload, signal: phaseSignal,
            request(user, operation, args = {}, extraSignal) {
              const work = (async () => {
                phaseSignal.throwIfAborted()
                const at = performance.now()
                const result = await adapter.request(user, operation, args, extraSignal ? AbortSignal.any([phaseSignal, extraSignal]) : phaseSignal)
                samples.add(result, performance.now() - at)
                phaseSignal.throwIfAborted()
                return result
              })()
              // Fault scenarios intentionally start work before waiting for SQL
              // observations. Keep cancellation handled until their later await.
              work.catch(() => {})
              return work
            },
            async control(action, args = {}) { phaseSignal.throwIfAborted(); const result = await adapter.control(action, args, phaseSignal); phaseSignal.throwIfAborted(); return result },
            wait: ms => delay(ms, undefined, { signal: phaseSignal }),
            observe(key, value) {
              phaseSignal.throwIfAborted()
              check(numericObservations.has(key) && Number.isFinite(value) && value >= 0 && value <= profile.runMs || booleanObservations.has(key) && typeof value === 'boolean', 'invalid_observation')
              phase.observations[key] = value
            },
          }
          await step(ctx)
        }, runSignal, profile.scenarioMs(name), 'scenario_timeout')
        phase.result = 'passed'
      } catch (error) { phase.result = 'failed'; phase.failure = { code: failureCode(error) }; throw error }
      finally { phase.duration_ms = rounded(performance.now() - since); phase.traffic = samples.snapshot(phase.duration_ms); await checkpoint() }
    }
    report.result = 'passed'
  } catch (error) {
    primary = failureCode(error)
    if (run.signal.aborted) primary = 'run_timeout'
    else if (signal?.aborted) primary = 'interrupted'
    report.result = primary === 'interrupted' ? 'interrupted' : 'failed'
    report.failure = { code: primary }
  } finally {
    clearTimeout(timer); run.abort()
    try {
      const cleanup = await scoped(s => adapter.cleanup(s), null, bounds.cleanupMs, 'cleanup_failed')
      check(cleanup?.complete === true, 'cleanup_failed')
      report.cleanup = { state: 'complete' }
    } catch { report.cleanup = { state: 'failed' }; report.result = 'failed'; report.failure ??= { code: 'cleanup_failed' } }
    report.finished_at = new Date().toISOString(); report.duration_ms = rounded(performance.now() - started)
    try { await checkpoint() } catch { report.result = 'failed'; report.failure = { code: 'report_write_failed' } }
  }
  return report
}
