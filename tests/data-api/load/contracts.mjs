import { writeFile, rename, rm } from 'node:fs/promises'
import { randomBytes } from 'node:crypto'

// Mirrors pkg/api/limits.go; contracts.test.mjs verifies the canonical bounds.
export const bounds = Object.freeze({ concurrency: 32, requests: 1000, users: 16, runMs: 180000, scenarioMs: 45000, cleanupMs: 30000, controlMs: 5000, sampleMs: 50, records: 4096, reportBytes: 65536 })
export const defaults = Object.freeze({ requests: 120, concurrency: 8, users: 4 })
export const pinnedEngine = Object.freeze({ version: '14.3', bytes: 20972200, sha256: '6fc07c4b166d54dabd5f2d2d5ba6e1f950385baaab0e5e24d29215e030a86839' })
export const phaseNames = Object.freeze(['mixed_load', 'admission_pressure', 'rate_pressure', 'deadline', 'disconnect', 'response_limits', 'lost_reply', 'engine_restart', 'recovery_load'])
const failures = new Set(['invalid_options', 'fixture_setup_failed', 'unsupported_runtime', 'source_identity_invalid', 'verification_failed', 'run_timeout', 'scenario_timeout', 'interrupted', 'cleanup_failed', 'report_write_failed', 'evidence_limit', 'record_limit', 'rls_violation', 'unexpected_http_result', 'admission_bounds_failed', 'pressure_not_observed', 'rate_limit_not_observed', 'rejected_write_executed', 'deadline_not_observed', 'sql_not_settled', 'receipt_not_reconciled', 'duplicate_write', 'engine_restart_failed', 'invalid_observation'])
export class LoadFailure extends Error {
  constructor(code) { super(failures.has(code) ? code : 'verification_failed'); this.code = this.message }
}
export function check(condition, code = 'verification_failed') { if (!condition) throw new LoadFailure(code) }
export function failureCode(error) { return error instanceof LoadFailure && failures.has(error.code) ? error.code : 'verification_failed' }
export function options(raw = {}) {
  check(raw && typeof raw === 'object' && !Array.isArray(raw) && Object.keys(raw).every(key => Object.hasOwn(defaults, key)), 'invalid_options')
  const value = { ...defaults, ...raw }
  for (const key of ['requests', 'concurrency', 'users']) check(Number.isInteger(value[key]) && value[key] >= (key === 'users' ? 2 : 1) && value[key] <= bounds[key], 'invalid_options')
  check(value.requests >= value.users * 4, 'invalid_options')
  return Object.freeze(value)
}
const codes = new Set(['data_api_overloaded', 'data_api_rate_limited', 'request_deadline_exceeded', 'query_timeout', 'response_too_large', 'data_api_unavailable', 'request_cancelled', 'request_timeout', 'token_invalid', 'token_required', 'no_response'])
export const safeCode = value => codes.has(value) ? value : value ? 'other' : 'none'
export const rounded = value => Math.round(value * 1000) / 1000
export class Samples {
  constructor() { this.values = []; this.statuses = {}; this.codes = {}; this.successes = 0; this.rejections = 0; this.transport = 0 }
  add(result, duration) {
    check(this.values.length < bounds.records, 'record_limit')
    check(Number.isFinite(duration) && duration >= 0)
    check(Number.isInteger(result.status) && (result.status === 0 || result.status >= 100 && result.status <= 599))
    this.values.push(Math.max(0, duration))
    const code = safeCode(result.code)
    this.statuses[result.status] = (this.statuses[result.status] ?? 0) + 1
    this.codes[code] = (this.codes[code] ?? 0) + 1
    if (result.status >= 200 && result.status < 300) this.successes++
    else if (result.status === 0) this.transport++
    else this.rejections++
  }
  snapshot(elapsed) {
    const sorted = [...this.values].sort((a, b) => a - b)
    const percentile = q => sorted.length ? rounded(sorted[Math.ceil(q * sorted.length) - 1]) : null
    return { requests: sorted.length, successes: this.successes, rejections: this.rejections, transport_outcomes: this.transport,
      statuses: { ...this.statuses }, codes: { ...this.codes }, latency_ms: { p50: percentile(.5), p95: percentile(.95), p99: percentile(.99) },
      completed_per_second: elapsed > 0 ? rounded(sorted.length * 1000 / elapsed) : null,
      successful_per_second: elapsed > 0 ? rounded(this.successes * 1000 / elapsed) : null }
  }
}
export function safeMetadata(value) {
  const output = {}
  for (const key of ['runtime_sha256', 'sdk_sha256', 'harness_sha256', 'engine_sha256']) { check(typeof value?.[key] === 'string' && /^[a-f0-9]{64}$/.test(value[key]), 'source_identity_invalid'); output[key] = value[key] }
  check(value.engine_version === pinnedEngine.version && value.engine_sha256 === pinnedEngine.sha256, 'unsupported_runtime')
  check(typeof value.node_version === 'string' && /^v\d+\.\d+\.\d+$/.test(value.node_version) && Number.isInteger(value.postgres_version_num) && value.postgres_version_num >= 160000 && value.postgres_version_num < 170000, 'unsupported_runtime')
  for (const key of ['fixture_sql_timeout_ms', 'rate_per_second', 'rate_burst']) check(Number.isInteger(value[key]) && value[key] > 0 && value[key] <= bounds.runMs, 'source_identity_invalid')
  return { ...output, engine_version: value.engine_version, node_version: value.node_version, postgres_version_num: value.postgres_version_num,
    fixture_sql_timeout_ms: value.fixture_sql_timeout_ms, rate_per_second: value.rate_per_second, rate_burst: value.rate_burst,
    restart_model: 'recreate_embedded_gateway_and_engine', production_supervision_qualified: false }
}
export async function writeEvidence(path, report) {
  const content = JSON.stringify(report, null, 2) + '\n'
  check(Buffer.byteLength(content) <= bounds.reportBytes, 'evidence_limit')
  const temporary = path + '.' + randomBytes(8).toString('hex') + '.tmp'
  try { await writeFile(temporary, content, { mode: 0o600, flag: 'wx' }); await rename(temporary, path) }
  catch { throw new LoadFailure('report_write_failed') }
  finally { await rm(temporary, { force: true }).catch(() => {}) }
}
