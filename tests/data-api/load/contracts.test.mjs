import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, readFile, readdir, rm, stat, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { bounds, defaults, options, pinnedEngine, safeMetadata, Samples, writeEvidence } from './contracts.mjs'
import { argumentsFor } from './verify.mjs'
import { LocalFixture, localSettings } from './local.mjs'
import { verifyLoad } from './runner.mjs'

const metadata = () => ({ runtime_sha256: 'a'.repeat(64), sdk_sha256: 'b'.repeat(64), harness_sha256: 'c'.repeat(64),
  engine_sha256: pinnedEngine.sha256, engine_version: pinnedEngine.version, node_version: 'v22.20.0', postgres_version_num: 160015,
  fixture_sql_timeout_ms: 30000, rate_per_second: 2, rate_burst: 8 })

test('workload and evidence bounds match the canonical Go limits', async () => {
  const source = await readFile(new URL('../../../pkg/api/limits.go', import.meta.url), 'utf8')
  const names = { concurrency: 'MaxConcurrency', requests: 'MaxRequests', users: 'MaxUsers', runMs: 'RunTimeoutMS', scenarioMs: 'ScenarioTimeoutMS', cleanupMs: 'CleanupTimeoutMS', controlMs: 'ControlTimeoutMS', sampleMs: 'SampleIntervalMS', records: 'MaxRecords', reportBytes: 'ReportMaxBytes' }
  for (const [name, suffix] of Object.entries(names)) assert.match(source, new RegExp(`DataAPILoad${suffix}\\s*=\\s*${bounds[name]}\\b`))
  assert.deepEqual(options(), defaults)
  assert.ok(Object.isFrozen(options()))
  for (const raw of [true, 120, '120', null, []]) assert.throws(() => options(raw), /invalid_options/)
  for (const raw of [{ requests: 0 }, { requests: 1001 }, { concurrency: 33 }, { concurrency: 1.5 }, { users: 1 }, { users: 17 }, { requests: 8, users: 4 }, { requests: '120' }, { constructor: 'private-secret' }, JSON.parse('{"__proto__":"private-secret"}')]) assert.throws(() => options(raw), /invalid_options/)
})

test('client samples count expected rejections separately and use completed client intervals', () => {
  const samples = new Samples()
  assert.equal(samples.snapshot(1).latency_ms.p95, null)
  for (const [status, code, elapsed] of [[200, null, 1], [429, 'data_api_rate_limited', 2], [503, 'data_api_overloaded', 10], [0, 'private-payload-code', 20]]) samples.add({ status, code, body: 'private-row-value' }, elapsed)
  const report = samples.snapshot(1000)
  assert.deepEqual(report.latency_ms, { p50: 2, p95: 20, p99: 20 })
  assert.equal(report.requests, 4); assert.equal(report.successes, 1); assert.equal(report.rejections, 2); assert.equal(report.transport_outcomes, 1)
  assert.equal(report.completed_per_second, 4); assert.equal(report.successful_per_second, 1)
  assert.equal(report.codes.other, 1); assert.doesNotMatch(JSON.stringify(report), /private-/)
  const bounded = new Samples()
  for (let i = 0; i < bounds.records; i++) bounded.add({ status: 200 }, 1)
  assert.throws(() => bounded.add({ status: 200 }, 1), /record_limit/)
})

test('source metadata drops private extras and rejects malformed or unpinned identities', () => {
  assert.doesNotMatch(JSON.stringify(safeMetadata({ ...metadata(), dsn: 'postgres://private-secret', tokens: ['private-token'] })), /private-|postgres:/)
  for (const change of [{ runtime_sha256: 'private-path' }, { engine_sha256: 'a'.repeat(64) }, { engine_version: '15.0' }, { postgres_version_num: 150000 }, { rate_burst: 'private-secret' }, { runtime_sha256: { toString: () => 'a'.repeat(64), toJSON: () => 'private-secret' } }]) assert.throws(() => safeMetadata({ ...metadata(), ...change }))
})

test('evidence writes are atomic and private, and oversized reports cannot replace valid evidence', async t => {
  const directory = await mkdtemp(join(tmpdir(), 'dapi-load-report-')); t.after(() => rm(directory, { recursive: true, force: true }))
  const file = join(directory, 'report.json')
  await writeFile(file, '{}', { mode: 0o644 })
  await writeEvidence(file, { result: 'passed' })
  assert.equal((await stat(file)).mode & 0o777, 0o600)
  const before = await readFile(file, 'utf8')
  await assert.rejects(writeEvidence(file, { extra: 'x'.repeat(bounds.reportBytes) }), /evidence_limit/)
  assert.equal(await readFile(file, 'utf8'), before)
  assert.deepEqual(await readdir(directory), ['report.json'])
})

test('entry options require an explicit output, bounded integers and a loopback administrative fixture', () => {
  assert.equal(argumentsFor(['--output', '/tmp/result.json']).workload.requests, defaults.requests)
  for (const args of [[], ['--output'], ['--output', '-'], ['--output', '/tmp/x', '--requests', '1e3'], ['--output', '/tmp/x', '--users', '2', '--users', '3'], ['--output', '/tmp/x', '--secret', 'private-token']]) assert.throws(() => argumentsFor(args), /invalid_options/)
  const env = { DATA_API_TEST_DATABASE_URL: 'postgres://fixture@127.0.0.1/test', DATA_API_POSTGREST_BIN: '/tmp/postgrest', DATA_API_RUNTIME_DIR: '/tmp/runtime' }
  assert.equal(new URL(localSettings(env).databaseURL).hostname, '127.0.0.1')
  assert.throws(() => localSettings({ ...env, DATA_API_TEST_DATABASE_URL: 'postgres://fixture@production.example/test' }), /invalid_options/)
  assert.throws(() => localSettings({ ...env, DATA_API_TEST_DATABASE_URL: env.DATA_API_TEST_DATABASE_URL + '?host=production.example' }), /invalid_options/)
})

test('an unpinned executable is rejected before execution or database access', async t => {
  const directory = await mkdtemp(join(tmpdir(), 'dapi-load-unpinned-')); t.after(() => rm(directory, { recursive: true, force: true }))
  const engine = join(directory, 'engine'), marker = join(directory, 'executed')
  await writeFile(engine, `#!/bin/sh\ntouch '${marker}'\n`, { mode: 0o755 })
  const fixture = new LocalFixture({ engine, runtime: directory, databaseURL: 'postgres://private-password@127.0.0.1:1/test' }, options())
  const report = await verifyLoad(fixture, options())
  assert.equal(report.result, 'failed'); assert.equal(report.failure.code, 'unsupported_runtime'); assert.equal(report.cleanup.state, 'complete')
  await assert.rejects(stat(marker), { code: 'ENOENT' })
  assert.doesNotMatch(JSON.stringify(report), /private-password|postgres:/)
})
