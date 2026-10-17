import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import https from 'node:https'
import { mkdtemp, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'

const command = promisify(execFile)
const enabled = Boolean(process.env.DATA_API_DEV_CLI && process.env.DATA_API_RUNTIME_DIR)

test('compiled health CLI validates runtime pool observations over trusted TLS and keeps credentials private', { skip: !enabled, timeout: 30000 }, async t => {
  const runtime = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/')
  const { createServer } = await import(new URL('server.mjs', runtime))
  const { runtimeConfig } = await import(new URL('config.mjs', runtime))
  const directory = await mkdtemp(join(tmpdir(), 'gregale-pool-health-'))
  t.after(() => rm(directory, { recursive: true, force: true }))
  const key = join(directory, 'key.pem'), cert = join(directory, 'cert.pem')
  await command('openssl', ['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', key, '-out', cert, '-days', '1', '-subj', '/CN=127.0.0.1', '-addext', 'subjectAltName=IP:127.0.0.1'])
  let waiting = 0, timeouts = 9, supported = true, publicReads = 0, managementReads = 0
  const engine = http.createServer((req, res) => {
    if (req.url !== '/metrics') return res.end('{}')
    if (!supported) { res.writeHead(404); return res.end('private-upstream-body') }
    res.end(`pgrst_db_pool_max 2.0\npgrst_db_pool_available 1.0\npgrst_db_pool_waiting ${waiting}.0\npgrst_db_pool_timeouts_total ${timeouts}.0\nprivate_metric{secret="private-metric-label"} 1\n`)
  })
  await new Promise(resolve => engine.listen(0, '127.0.0.1', resolve))
  const config = runtimeConfig({ DATABASE_URL: 'postgres://restricted:test-only@localhost/db', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes' })
  const gateway = createServer(config, async () => { throw Error('health must not verify JWTs') }, engine.address().port, engine.address().port, () => {})
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  const serving = https.createServer({ key: await readFile(key), cert: await readFile(cert) }, (req, res) => {
    publicReads++
    assert.equal(req.headers.authorization, undefined)
    assert.equal(req.headers.cookie, undefined)
    const forwarded = http.get({ hostname: '127.0.0.1', port: gateway.address().port, path: req.url }, response => {
      res.writeHead(response.statusCode, response.headers); response.pipe(res)
    })
    forwarded.on('error', () => res.destroy())
  })
  await new Promise(resolve => serving.listen(0, '127.0.0.1', resolve))
  const management = http.createServer((req, res) => {
    managementReads++
    assert.equal(req.headers.authorization, 'Bearer private-owner-token')
    if (req.url === '/v1/apps/notes') return res.end(JSON.stringify({ slug: 'notes', canonical_url: `https://127.0.0.1:${serving.address().port}` }))
    if (req.url === '/v1/apps/notes/data-api/connection-budget') return res.end(JSON.stringify({ version: 1, app: 'notes', state: 'not_applicable', truncated: false, databases: [] }))
    assert.ok(req.url.startsWith('/v1/apps/notes/logs?'))
    res.writeHead(200, { 'Content-Type': 'text/event-stream' })
    res.end('event: end\ndata: {}\n\n')
  })
  await new Promise(resolve => management.listen(0, '127.0.0.1', resolve))
  t.after(async () => {
    for (const server of [serving, gateway, engine, management]) server.closeAllConnections()
    await Promise.all([serving, gateway, engine, management].map(server => new Promise(resolve => server.close(resolve))))
  })
  const env = { ...process.env, FAAS_API: `http://127.0.0.1:${management.address().port}`, FAAS_TOKEN: 'private-owner-token', SSL_CERT_FILE: cert }
  for (const scenario of ['healthy', 'waiting', 'unsupported', 'recovered']) {
    waiting = scenario === 'waiting' ? 2 : 0
    supported = scenario !== 'unsupported'
    if (scenario === 'recovered') timeouts = 0
    const result = await command(process.env.DATA_API_DEV_CLI, ['--json', 'data-api', 'health', 'notes', '--timeout', '10s'], { env, timeout: 15000 }).then(value => ({ ...value, code: 0 }), error => ({ stdout: error.stdout, stderr: error.stderr, code: error.code }))
    const report = JSON.parse(result.stdout)
    assert.equal(result.code, scenario === 'healthy' || scenario === 'recovered' ? 0 : 1)
    assert.equal(report.readiness.state, 'ok')
    assert.equal(report.readiness.ready, true)
    assert.equal(report.partial, scenario === 'unsupported')
    if (scenario === 'unsupported') {
      assert.equal(report.readiness.pool.state, 'unknown')
      assert.equal(report.readiness.pool.reason, 'unsupported')
      assert.equal(report.readiness.pool.available, undefined)
    } else {
      assert.equal(report.readiness.pool.state, 'observed')
      assert.equal(report.readiness.pool.waiting, waiting)
      assert.equal(report.readiness.pool.timeouts_total, timeouts)
      assert.ok(Number.isFinite(Date.parse(report.readiness.pool.checked_at)))
    }
    assert.equal(report.current_findings.includes('pool_waiting'), scenario === 'waiting')
    assert.doesNotMatch(result.stdout + result.stderr, /private-owner-token|private-upstream-body|private-metric-label|test-only/)
  }
  const human = await command(process.env.DATA_API_DEV_CLI, ['data-api', 'health', 'notes'], { env, timeout: 15000 })
  assert.match(human.stdout, /Pool: capacity 2; available 1; waiting 0; cumulative acquisition timeouts 0; checked at/)
  assert.equal(publicReads, 10)
  assert.equal(managementReads, 15)
})
