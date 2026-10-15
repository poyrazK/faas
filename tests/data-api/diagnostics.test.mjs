import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { traceRequest, markRequest } from '../../cmd/gregale/templates/data-api/logging.mjs'

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
  ]
  let index = 0
  const server = http.createServer((req, res) => {
    traceRequest(req, res, event => events.push(event))
    const [, status, code] = cases[index++]
    if (code) markRequest(res, code)
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
  assert.equal(report.summary.requests, 7)
  assert.equal(report.summary.errors, 5)
  assert.equal(report.summary.server_errors, 3)
  assert.equal(report.data_requests.requests, 6)
  assert.equal(report.authentication.requests, 1)
  assert.equal(report.by_route.health.requests, 1)
  assert.equal(report.ignored, 1)
  assert.deepEqual(report.failures, { authentication: 1, timeouts: 1, unavailable: 1, contract_mismatch: 1, upstream: 1 })
  assert.ok(report.summary.latency_ms.p50 >= 0)
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
