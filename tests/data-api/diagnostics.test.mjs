import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { traceRequest, markRequest, markAdmission, markDeadline, markPhase, markQueryShape } from '../../cmd/gregale/templates/data-api/logging.mjs'

const command = promisify(execFile)

test('diagnostics consumes real runtime request records without exposing application logs', { skip: !process.env.DATA_API_DEV_CLI, timeout: 15000 }, async t => {
  const events = []
  const cases = [
    ['/rest/v1/notes', 200, null],
    ['/rest/v1/rpc/echo', 500, 'upstream_error'],
    ['/rest/v1/notes', 401, 'token_required'],
    ['/rest/v1/notes', 504, 'query_timeout'],
    ['/rest/v1/notes', 409, 'schema_contract_mismatch'],
    ['/rest/v1/notes', 503, 'data_api_unavailable'],
    ['/healthz', 200, null],
    ['/rest/v1/notes', 503, 'data_api_overloaded'],
    ['/rest/v1/notes', 503, 'data_api_overloaded'],
    ['/livez', 200, null],
    ['/rest/v1/notes', 503, 'data_api_refresh_paused'],
    ['/rest/v1/notes', 503, 'data_api_refresh_unavailable'],
    ['/rest/v1/notes', 504, 'request_deadline_exceeded'],
  ]
  let index = 0
  const server = http.createServer((req, res) => {
    traceRequest(req, res, event => events.push(event))
    const [, status, code] = cases[index++]
    if (code) markRequest(res, code)
    if (index === 1) markAdmission(res, { outcome: 'admitted', wait_ms: 1, active: 1, queued: 0 })
    if (index === 1) { markPhase(res, 'verification'); markPhase(res, 'upstream'); markPhase(res, 'streaming') }
    if (index === 8) markAdmission(res, { outcome: 'queue_full', wait_ms: 0, active: 2, queued: 8 })
    if (index === 9) markAdmission(res, { outcome: 'queue_timeout', wait_ms: 1000, active: 2, queued: 0 })
    if (code === 'request_deadline_exceeded') { markPhase(res, 'verification'); markDeadline(res, { phase: 'verification', elapsed_ms: 15000, dispatched: false }) }
    res.statusCode = status
    res.end('private-response-body')
  })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  t.after(() => { server.closeAllConnections(); server.close() })
  for (const [path] of cases) {
    const response = await fetch(`http://127.0.0.1:${server.address().port}${path}`, { headers: { Authorization: 'Bearer private-jwt', 'X-Private-Subject': 'private-subject' } })
    await response.text()
  }
  assert.equal(events.length, cases.length)
  const directory = await mkdtemp(join(tmpdir(), 'gregale-diagnostics-records-'))
  t.after(() => rm(directory, { recursive: true, force: true }))
  const file = join(directory, 'logs.ndjson')
  await writeFile(file, events.map(event => JSON.stringify({ seq: 1, instance: 'test-instance', stream: 'stderr', line: JSON.stringify(event) })).join('\n') + '\n' + JSON.stringify({ event: 'application_event', authorization: 'private-jwt', sql: 'private-sql', body: 'private-body' }) + '\n')
  const { stdout } = await command(process.env.DATA_API_DEV_CLI, ['--json', 'data-api', 'diagnostics', '--file', file], { timeout: 10000, env: { ...process.env, FAAS_TOKEN: '', FAAS_API: 'http://127.0.0.1:1' } })
  const report = JSON.parse(stdout)
  assert.equal(report.partial, false)
  assert.equal(report.coverage, 'provided_file')
  assert.equal(report.summary.requests, 13)
  assert.equal(report.summary.errors, 10)
  assert.equal(report.summary.server_errors, 8)
  assert.equal(report.data_requests.requests, 11)
  assert.equal(report.authentication.requests, 1)
  assert.equal(report.by_route.health.requests, 2)
  assert.equal(report.ignored, 1)
  assert.deepEqual(report.failures, { authentication: 1, timeouts: 2, unavailable: 3, contract_mismatch: 1, upstream: 1, overloaded: 2, refresh_paused: 1, refresh_unavailable: 1, response_too_large: 0, rate_limited: 0, concurrency_limited: 0 })
  assert.deepEqual(report.admission, { observed: 3, admitted: 1, queue_full: 1, queue_timeout: 1, cancelled: 0, peak_active: 2, peak_queued: 8, wait_ms: { p50: 1, p95: 1000, p99: 1000 } })
  assert.deepEqual(report.deadlines, { observed: 1, dispatched: 0, by_phase: { verification: 1 } })
  assert.ok(report.summary.latency_ms.p50 >= 0)
  assert.equal(report.summary.timings.observed_requests, 2)
  assert.equal(report.summary.timings.without_timings, 11)
  assert.equal(report.summary.timings.by_phase.verification.completed, 1)
  assert.equal(report.summary.timings.by_phase.verification.interrupted, 1)
  assert.equal(report.summary.timings.by_phase.upstream.completed, 1)
  assert.doesNotMatch(stdout, /private-jwt|private-subject|private-sql|private-body|private-response-body|notes|echo/)
  const auth = events.find(event => event.code === 'token_required')
  const lookup = await command(process.env.DATA_API_DEV_CLI, ['--json', 'data-api', 'diagnostics', '--file', file, '--request-id', auth.request_id], { timeout: 10000, env: { ...process.env, FAAS_TOKEN: '' } })
  const selected = JSON.parse(lookup.stdout)
  assert.equal(selected.summary.requests, 1)
  assert.equal(selected.matches[0].code, 'token_required')
  assert.equal(selected.matches[0].request_id, auth.request_id)
})


test('diagnostics retains read-only rejections without leaking signed identity metadata',{skip:!process.env.DATA_API_DEV_CLI},async t=>{
 const directory=await mkdtemp(join(tmpdir(),'gregale-read-only-diagnostics-'));t.after(()=>rm(directory,{recursive:true,force:true}))
 const events=[],server=http.createServer((req,res)=>{traceRequest(req,res,event=>events.push(event));markRequest(res,'read_only_identity');res.statusCode=403;res.end('{}')})
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));t.after(()=>{server.closeAllConnections();server.close()})
 await(await fetch(`http://127.0.0.1:${server.address().port}/rest/v1/private-table`,{method:'POST',headers:{Authorization:'Bearer private-token'}})).text()
 const path=join(directory,'log.ndjson');await writeFile(path,events.map(event=>JSON.stringify(event)).join('\n'))
 const {stdout}=await command(process.env.DATA_API_DEV_CLI,['--json','data-api','diagnostics','--file',path],{env:{...process.env,FAAS_TOKEN:''}})
 const result=JSON.parse(stdout);assert.equal(result.summary.requests,1);assert.equal(result.summary.errors,1);assert.equal(result.partial,false);assert.doesNotMatch(stdout,/private-token|private-table/)
})

test('CLI ranks redacted runtime query shapes by latency or failures with phase coverage', { skip: !process.env.DATA_API_DEV_CLI }, async t => {
  const directory = await mkdtemp(join(tmpdir(), 'gregale-shape-timings-'))
  t.after(() => rm(directory, { recursive: true, force: true }))
  const events = [], cases = [
    ['id', 10, 200], ['id', 20, 200], ['id', 30, 200],
    ['subject', 300, 200], ['id,subject', 40, 500], ['id,subject', 50, 500],
  ]
  const config = { queryShapes: true, fingerprint: 'a'.repeat(64), schemas: ['api'], snapshot: { tables: [{ schema: 'api', name: 'notes', columns: [{ name: 'id' }, { name: 'subject' }] }] } }
  let now = 0, index = 0
  t.mock.method(performance, 'now', () => now)
  const server = http.createServer((req, res) => {
    const [, duration, status] = cases[index++]
    traceRequest(req, res, record => events.push(record))
    markQueryShape(req, res, config)
    markPhase(res, 'verification'); now++
    markPhase(res, 'upstream'); now += duration - 1
    markPhase(res, 'streaming')
    if (status === 500) markRequest(res, 'upstream_error')
    res.statusCode = status; res.end('private-response')
  })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  t.after(() => { server.closeAllConnections(); server.close() })
  for (const [projection] of cases) {
    await (await fetch(`http://127.0.0.1:${server.address().port}/rest/v1/notes?select=${projection}&subject=eq.private-filter`, { headers: { Authorization: 'Bearer private-jwt' } })).text()
  }
  const file = join(directory, 'logs.ndjson')
  await writeFile(file, [...events, events[0]].map(event => JSON.stringify(event)).join('\n'))
  for (const [order, expected] of [['requests', ['id']], ['p95', ['subject']], ['errors', ['id', 'subject']]]) {
    const { stdout } = await command(process.env.DATA_API_DEV_CLI, ['--json', 'data-api', 'diagnostics', '--file', file, '--sort', order], { env: { ...process.env, FAAS_TOKEN: '' } })
    const report = JSON.parse(stdout)
    assert.equal(report.partial, false)
    assert.equal(report.query_shape_sort, order)
    assert.equal(report.duplicates, 1)
    assert.deepEqual(report.query_shapes[0].query_shape.projection, expected)
    assert.equal(report.summary.timings.observed_requests, 6)
    assert.equal(report.summary.timings.without_timings, 0)
    assert.equal(report.query_shapes[0].stats.timings.by_phase.upstream.completed, report.query_shapes[0].stats.requests)
    assert.equal(report.query_shapes[0].stats.timings.by_phase.streaming.latency_ms.p95, 0)
    assert.doesNotMatch(stdout, /private-filter|private-jwt|private-response/)
  }
})
