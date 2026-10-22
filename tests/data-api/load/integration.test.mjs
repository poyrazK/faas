import test from 'node:test'
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { spawn } from 'node:child_process'
import { mkdtemp, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'
import { pathToFileURL } from 'node:url'
import { LoadFailure, options } from './contracts.mjs'
import { LocalFixture, localSettings } from './local.mjs'
import { verifyLoad } from './runner.mjs'

const enabled = process.env.DATA_API_LOAD_FAILURE_TESTS === '1'
async function inspector(t, settings) {
  const require = createRequire(pathToFileURL(join(settings.runtime, 'package.json')))
  const client = new (require('pg').Client)({ connectionString: settings.databaseURL, connectionTimeoutMillis: 5000, statement_timeout: 5000 })
  await client.connect(); t.after(() => client.end())
  return client
}
async function absent(client, runID) {
  assert.match(runID, /^[a-f0-9]{24}$/)
  assert.equal((await client.query('SELECT 1 FROM pg_database WHERE datname=$1', [`dapi_load_${runID}`])).rowCount, 0)
  assert.equal((await client.query('SELECT 1 FROM pg_roles WHERE rolname=$1', [`dapi_role_${runID}`])).rowCount, 0)
  assert.equal((await client.query('SELECT 1 FROM pg_stat_activity WHERE usename=$1', [`dapi_role_${runID}`])).rowCount, 0)
}

test('real fixture cleans blocked SQL and owned resources after a failed assertion', { skip: !enabled, timeout: 60000 }, async t => {
  const settings = localSettings(process.env), workload = options(), fixture = new LocalFixture(settings, workload)
  const client = await inspector(t, settings)
  let pending
  const report = await verifyLoad(fixture, workload, { signal: t.signal, steps: [['mixed_load', async ctx => {
    await ctx.control('lock')
    pending = ctx.request('failure-test', 'write', { key: 'blocked-write' }).catch(() => {})
    await ctx.control('wait_lock', { count: 1 })
    throw new LoadFailure('pressure_not_observed')
  }]] })
  await pending
  assert.equal(report.result, 'failed'); assert.equal(report.failure.code, 'pressure_not_observed')
  assert.equal(report.cleanup.state, 'complete'); await absent(client, report.run_id)
  assert.doesNotMatch(JSON.stringify(report), /postgres:|fixture-value|failure-test|blocked-write/)
})

test('SIGTERM during real admission pressure saves an interrupted report and removes owned resources', { skip: !enabled, timeout: 90000 }, async t => {
  const settings = localSettings(process.env), client = await inspector(t, settings)
  const directory = await mkdtemp(join(tmpdir(), 'dapi-load-interrupt-')); let verified = false
  const output = join(directory, 'report.json')
  t.after(() => verified ? rm(directory, { recursive: true, force: true }) : t.diagnostic(`Retained interruption evidence: ${output}`))
  const child = spawn(process.execPath, [new URL('verify.mjs', import.meta.url).pathname, '--output', output], { stdio: ['ignore', 'pipe', 'pipe'] })
  child.stdout.resume(); child.stderr.resume()
  const exited = new Promise((resolveExit, reject) => { child.once('error', reject); child.once('exit', (code, signal) => resolveExit({ code, signal })) })
  t.after(async () => {
    if (child.exitCode !== null || child.signalCode !== null) return
    child.kill('SIGTERM')
    const timer = new AbortController()
    try { await Promise.race([exited, delay(35000, undefined, { signal: timer.signal }).then(() => child.kill('SIGKILL'))]) }
    finally { timer.abort() }
  })
  const signal = AbortSignal.timeout(60000)
  let runID
  while (true) {
    signal.throwIfAborted()
    const report = await readFile(output, 'utf8').then(JSON.parse).catch(() => null)
    runID = report?.run_id
    if (report?.scenarios.at(-1)?.name === 'admission_pressure') {
      const rows = (await client.query("SELECT 1 FROM pg_stat_activity WHERE usename=$1 AND wait_event_type='Lock'", [`dapi_role_${runID}`])).rowCount
      if (rows > 0) break
    }
    if (child.exitCode !== null) assert.fail('verifier exited before interruption')
    await delay(20, undefined, { signal })
  }
  child.kill('SIGTERM')
  assert.deepEqual(await exited, { code: 1, signal: null })
  const report = JSON.parse(await readFile(output, 'utf8'))
  assert.equal(report.result, 'interrupted'); assert.equal(report.failure.code, 'interrupted')
  assert.equal(report.cleanup.state, 'complete'); await absent(client, runID)
  verified = true
})
