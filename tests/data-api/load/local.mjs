import { createHash, randomBytes } from 'node:crypto'
import { spawn, execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { createRequire } from 'node:module'
import { createReadStream } from 'node:fs'
import { readFile, readdir, stat } from 'node:fs/promises'
import { join, resolve } from 'node:path'
import { pathToFileURL, fileURLToPath } from 'node:url'
import net from 'node:net'
import { setTimeout as delay } from 'node:timers/promises'
import { bounds, check, LoadFailure, pinnedEngine, safeCode } from './contracts.mjs'

const command = promisify(execFile), here = fileURLToPath(new URL('.', import.meta.url))
const root = resolve(here, '../../..'), sdkRoot = join(root, 'sdk/data/dist')
async function engineHash(path) {
  const info = await stat(path); check(info.isFile() && info.size === pinnedEngine.bytes, 'unsupported_runtime')
  const hash = createHash('sha256')
  for await (const chunk of createReadStream(path)) hash.update(chunk)
  return hash.digest('hex')
}
async function boundedText(response, maximum) {
  const parts = []; let bytes = 0
  for await (const part of response.body ?? []) { bytes += part.length; check(bytes <= maximum, 'unexpected_http_result'); parts.push(part) }
  return Buffer.concat(parts).toString('utf8')
}
async function treeHash(directory, extensions) {
  const hash = createHash('sha256')
  for (const name of (await readdir(directory)).filter(name => extensions.some(extension => name.endsWith(extension))).sort()) {
    hash.update(name); hash.update('\0'); hash.update(await readFile(join(directory, name))); hash.update('\0')
  }
  return hash.digest('hex')
}
async function freePort() {
  const server = net.createServer(); await new Promise((r, j) => { server.once('error', j); server.listen(0, '127.0.0.1', r) })
  const number = server.address().port; await new Promise(r => server.close(r)); return number
}
export function localSettings(env) {
  let url
  try { url = new URL(env.DATA_API_TEST_DATABASE_URL) } catch { throw new LoadFailure('invalid_options') }
  check(['postgres:', 'postgresql:'].includes(url.protocol) && ['127.0.0.1', 'localhost', '[::1]'].includes(url.hostname) && Boolean(env.DATA_API_RUNTIME_DIR && env.DATA_API_POSTGREST_BIN), 'invalid_options')
  // pg accepts connection-target overrides in URL query parameters. Keep the
  // loopback restriction effective for the parsed connection, too.
  check([...url.searchParams.keys()].every(key => key === 'sslmode') && url.searchParams.getAll('sslmode').length <= 1, 'invalid_options')
  return { databaseURL: url.toString(), runtime: resolve(env.DATA_API_RUNTIME_DIR), engine: resolve(env.DATA_API_POSTGREST_BIN) }
}

export class LocalFixture {
  constructor(settings, workload) {
    Object.assign(this, { settings, workload, records: [], connectionSamples: [], tokens: new Map(), inFlight: new Set(), allRequests: new AbortController() })
    this.created = { role: false, database: false }; this.arrivals = 0; this.rate = { perSecond: 2, burst: 8 }
  }
  async setup(runID, signal) {
    this.runID = runID; this.database = `dapi_load_${runID}`; this.role = `dapi_role_${runID}`
    check(/^[a-f0-9]{24}$/.test(runID), 'fixture_setup_failed')
    const binaryHash = await engineHash(this.settings.engine); check(binaryHash === pinnedEngine.sha256, 'unsupported_runtime')
    signal.throwIfAborted()
    const version = await command(this.settings.engine, ['--version'], { timeout: bounds.controlMs, maxBuffer: 8192 }).catch(() => { throw new LoadFailure('unsupported_runtime') })
    check(/^PostgREST 14\.3(?:\s|$)/.test(version.stdout), 'unsupported_runtime')
    signal.throwIfAborted()
    const runtimeURL = pathToFileURL(this.settings.runtime + '/')
    const require = createRequire(new URL('package.json', runtimeURL)); this.pg = require('pg')
    const jose = await import(pathToFileURL(require.resolve('jose')))
    this.jose = jose; this.keys = await jose.generateKeyPair('ES256')
    this.configModule = await import(new URL('config.mjs', runtimeURL)); this.serverModule = await import(new URL('server.mjs', runtimeURL)); this.catalog = await import(new URL('types.mjs', runtimeURL))
    this.sdk = await import(pathToFileURL(join(sdkRoot, 'index.js'))); this.limits = this.configModule.limits
    signal.throwIfAborted()
    check(this.limits.pool === 2 && this.limits.queuedRequests === 8 && this.limits.queryMs === 15000 && this.limits.responseBytes === 16777216, 'unsupported_runtime')
    this.admin = this.client(this.settings.databaseURL); await this.admin.connect()
    const postgres = Number((await this.admin.query('SHOW server_version_num')).rows[0].server_version_num)
    check(postgres >= 160000 && postgres < 170000, 'unsupported_runtime'); signal.throwIfAborted()
    const password = randomBytes(24).toString('hex')
    this.pendingCreation = true
    await this.admin.query(`CREATE ROLE "${this.role}" LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD '${password}'`); this.created.role = true; this.pendingCreation = false
    signal.throwIfAborted()
    await this.admin.query(`ALTER ROLE "${this.role}" SET statement_timeout=${this.limits.queryMs * 2}`)
    signal.throwIfAborted()
    this.pendingCreation = true
    await this.admin.query(`CREATE DATABASE "${this.database}"`); this.created.database = true; this.pendingCreation = false
    signal.throwIfAborted()
    const ownerURL = new URL(this.settings.databaseURL); ownerURL.pathname = '/' + this.database
    this.owner = this.client(ownerURL.toString()); await this.owner.connect()
    const sql = (await readFile(join(here, 'fixture.sql'), 'utf8')).replaceAll('__ROLE__', '"' + this.role + '"').replaceAll('__RESPONSE_BYTES__', String(this.limits.responseBytes))
    await this.owner.query(sql)
    for (let i = 0; i < this.workload.users; i++) { signal.throwIfAborted(); await this.owner.query('INSERT INTO api.notes VALUES($1,$2,$3)', [`seed-${i}`, this.subject(i), 'fixture-seed']) }
    signal.throwIfAborted()
    await this.owner.query('INSERT INTO api.large_payload VALUES($1,repeat($2,$3))', [this.subject('large'), 'x', this.limits.responseBytes + 1])
    const login = new URL(ownerURL); login.username = this.role; login.password = password
    this.login = login.toString(); this.upstreamPort = await freePort(); this.readyPort = await freePort(); this.gatewayPort = await freePort()
    await this.start(signal)
    this.sampler = setInterval(() => {
      if (this.sampling) return
      this.sampling = this.state().then(value => {
        if (this.connectionSamples.length >= bounds.records) this.recordOverflow = true
        else this.connectionSamples.push(value.connections)
      }).catch(() => { this.samplingFailed = true }).finally(() => { this.sampling = null })
    }, bounds.sampleMs)
    return { runtime_sha256: await treeHash(this.settings.runtime, ['.mjs', '.json', 'Dockerfile']), sdk_sha256: await treeHash(sdkRoot, ['.js', '.ts']), harness_sha256: await treeHash(here, ['.mjs', '.sql']),
      engine_version: pinnedEngine.version, engine_sha256: binaryHash, node_version: process.version, postgres_version_num: postgres,
      fixture_sql_timeout_ms: this.limits.queryMs * 2, rate_per_second: this.rate.perSecond, rate_burst: this.rate.burst }
  }
  client(connectionString) { const client = new this.pg.Client({ connectionString, connectionTimeoutMillis: bounds.controlMs, statement_timeout: bounds.controlMs, query_timeout: bounds.controlMs + 1000 }); client.on('error', () => { this.clientFailure = true }); return client }
  subject(user) { return `fixture-${this.runID}-${user}` }
  async token(user) {
    if (!this.tokens.has(user)) this.tokens.set(user, new this.jose.SignJWT({ sub: this.subject(user) }).setProtectedHeader({ alg: 'ES256' }).setIssuer('https://issuer.invalid').setAudience('load-fixture').setExpirationTime('1h').sign(this.keys.privateKey))
    return this.tokens.get(user)
  }
  async until(read, ready, signal, timeout = 10000) {
    const end = performance.now() + timeout
    while (performance.now() < end) {
      signal.throwIfAborted()
      const value = await read()
      if (ready(value)) return value
      await delay(20, undefined, { signal })
    }
    throw new LoadFailure('verification_failed')
  }
  async start(signal) {
    signal.throwIfAborted()
    const config = this.configModule.runtimeConfig({ DATABASE_URL: this.login, DATA_API_ISSUER: 'https://issuer.invalid', DATA_API_JWKS_URL: 'https://issuer.invalid/jwks', DATA_API_AUDIENCE: 'load-fixture', DATA_API_RATE_LIMIT_PER_SECOND: String(this.rate.perSecond), DATA_API_RATE_LIMIT_BURST: String(this.rate.burst) })
    config.snapshot = await this.catalog.inspect(config.connection, config.schemas); config.fingerprint = this.catalog.fingerprint(config.snapshot); config.functions = config.snapshot.functions
    signal.throwIfAborted()
    this.engineStartFailed = false
    this.child = spawn(this.settings.engine, [], { env: { ...process.env, ...config.postgrestEnv, PGRST_SERVER_PORT: String(this.upstreamPort), PGRST_ADMIN_SERVER_PORT: String(this.readyPort) }, stdio: 'ignore' })
    this.childExited = new Promise(resolve => { this.child.once('exit', resolve); this.child.once('error', () => { this.engineStartFailed = true; resolve() }) })
    await this.until(async () => {
      if (this.engineStartFailed || this.child.exitCode !== null || this.child.signalCode !== null) throw new LoadFailure('engine_restart_failed')
      try { return (await fetch(`http://127.0.0.1:${this.readyPort}/ready`, { signal: AbortSignal.any([signal, AbortSignal.timeout(2000)]), redirect: 'error' })).status } catch { return 0 }
    }, status => status === 200, signal)
    signal.throwIfAborted()
    this.gateway = this.serverModule.createServer(config, this.serverModule.tokenVerifier(config.auth, this.keys.publicKey), this.upstreamPort, this.readyPort, record => this.capture(record))
    this.gateway.on('request', () => this.arrivals++)
    await new Promise((r, j) => { this.gateway.once('error', j); this.gateway.listen(this.gatewayPort, '127.0.0.1', r) })
    this.base = `http://127.0.0.1:${this.gatewayPort}`
    // The fixture recreates both components on exit. This is not an attestation
    // of main()'s process supervision or native VM lifecycle behavior.
    this.child.once('exit', () => { this.gateway?.closeAllConnections(); this.gateway?.close() })
  }
  capture(record) {
    if (this.records.length >= bounds.records) { this.recordOverflow = true; return }
    this.records.push({ request_id: record.request_id, status: record.status, outcome: record.outcome, code: safeCode(record.code),
      admission: record.admission && { active: record.admission.active, queued: record.admission.queued, outcome: record.admission.outcome },
      deadline: Boolean(record.deadline), response_limit: Boolean(record.response_limit) })
  }
  async request(user, operation, args, signal) {
    const work = this.performRequest(user, operation, args, AbortSignal.any([signal, this.allRequests.signal]))
    this.inFlight.add(work)
    try { return await work } finally { this.inFlight.delete(work) }
  }
  async performRequest(user, operation, args, signal) {
    signal.throwIfAborted()
    let attempts = 0, requestID, clientBoundExceeded = false
    const accessToken = await this.token(user)
    const transport = async (input, init) => {
      attempts++
      const response = await fetch(input, { ...init, redirect: 'error' })
      requestID = response.headers.get('x-request-id')
      if (args.loseReply) { await response.body?.cancel(); throw new TypeError('Fixture response discarded') }
      if (!response.body) return response
      const reader = response.body.getReader(), thisFixture = this; let bytes = 0
      const body = new ReadableStream({
        async pull(controller) {
          try {
            const next = await reader.read()
            if (next.done) { controller.close(); reader.releaseLock(); return }
            bytes += next.value.length
            if (bytes > thisFixture.limits.responseBytes) { clientBoundExceeded = true; await reader.cancel(); controller.error(new TypeError('Fixture response bound')); return }
            controller.enqueue(next.value)
          } catch (error) { controller.error(error) }
        }, cancel: reason => reader.cancel(reason),
      })
      return new Response(body, { status: response.status, headers: response.headers })
    }
    const db = this.sdk.createDataClient({ url: this.base, accessToken, fetch: transport }).schema('api')
    let query
    switch (operation) {
      case 'read': query = db.from('notes').select('key,subject,body').order('key'); break
      case 'rpc_read': query = db.rpc('visible_notes'); break
      case 'rest_write': query = db.from('notes').insert({ key: args.key, subject: this.subject(user), body: args.body ?? 'fixture-value' }).select(); break
      case 'write': query = db.rpc('write_note', { note_key: args.key, payload: args.body ?? 'fixture-value' }); break
      case 'receipt': query = db.from('notes').select('key,subject,body').eq('key', args.key); break
      case 'ping': query = db.rpc('whoami'); break
      case 'pause': query = db.rpc('pause_ping'); break
      case 'large_read': query = db.from('large_payload').select('body'); break
      case 'large_write': query = db.rpc('commit_large', { note_key: args.key }); break
      default: throw new LoadFailure('verification_failed')
    }
    const result = await this.sdk.dataRequest(query, { timeoutMs: this.limits.queryMs * 2, signal })
    const rows = result.data == null ? [] : Array.isArray(result.data) ? result.data : [result.data]
    const classification = this.sdk.classifyDataError(result.error, { status: result.status })
    return { status: result.status, code: result.error?.code || (result.status === 0 ? 'no_response' : null), attempts, request_id: requestID,
      owned: rows.every(row => row?.subject === this.subject(user)), rows: rows.length, client_bound_exceeded: clientBoundExceeded, category: classification?.category }
  }
  async state() {
    const rows = (await this.admin.query("SELECT state,wait_event_type,position('pause_ping' in query)>0 AS pausing FROM pg_stat_activity WHERE usename=$1", [this.role])).rows
    return { connections: rows.length, active: rows.filter(r => r.state !== 'idle').length, locked: rows.filter(r => r.wait_event_type === 'Lock').length, pausing: rows.some(r => r.state === 'active' && r.pausing) }
  }
  async health(path, signal) {
    try {
      const response = await fetch(this.base + path, { signal: AbortSignal.any([signal, AbortSignal.timeout(this.limits.healthProbeMs + 2000)]), redirect: 'error' })
      const text = await boundedText(response, this.limits.healthBytes)
      const data = JSON.parse(text)
      return { status: response.status, ready: data.ready, live: data.live, engine: data.checks?.engine, sql: data.checks?.sql, pool: data.pool?.state }
    } catch (error) { if (error instanceof LoadFailure) throw error; return { status: 0 } }
  }
  async control(action, args, signal) {
    signal.throwIfAborted()
    switch (action) {
      case 'profile': return { pool: this.limits.pool, queue: this.limits.queuedRequests, queryMs: this.limits.queryMs, ...this.rate }
      case 'record_mark': return { records: this.records.length, samples: this.connectionSamples.length }
      case 'arrival_count': return this.arrivals
      case 'wait_arrivals': return this.until(() => this.arrivals, count => count >= args.count, signal)
      case 'lock': await this.owner.query('BEGIN; LOCK TABLE api.notes IN ACCESS EXCLUSIVE MODE'); return true
      case 'unlock': await this.owner.query('COMMIT'); return true
      case 'wait_lock': return this.until(() => this.state(), state => state.locked >= args.count, signal)
      case 'wait_pause': return this.until(() => this.state(), state => state.pausing, signal)
      case 'settle': return this.until(() => this.state(), state => state.active === 0, signal)
      case 'state': return this.state()
      case 'health': return this.health('/healthz', signal)
      case 'live': return this.health('/livez', signal)
      case 'healthy': return this.until(() => this.health('/healthz', signal), state => state.status === 200 && state.ready, signal)
      case 'invocations': { const row = (await this.owner.query('SELECT last_value,is_called FROM api.invocations')).rows[0]; return row.is_called ? Number(row.last_value) : 0 }
      case 'count': return Number((await this.owner.query('SELECT count(*) FROM api.notes WHERE key=$1', [args.key])).rows[0].count)
      case 'total': return Number((await this.owner.query('SELECT count(*) FROM api.notes')).rows[0].count)
      case 'verify_writes': {
        const rows = (await this.owner.query('SELECT key FROM api.notes WHERE key=ANY($1::text[])', [[...args.accepted, ...args.rejected]])).rows.map(row => row.key)
        return rows.length === args.accepted.length && rows.every(key => args.accepted.includes(key))
      }
      case 'statistics': {
        check(!this.recordOverflow, 'record_limit'); check(!this.samplingFailed && !this.clientFailure)
        const rows = this.records.slice(args.since?.records ?? 0), samples = this.connectionSamples.slice(args.since?.samples ?? 0)
        return { peak_active: Math.max(0, ...rows.map(r => r.admission?.active ?? 0)), peak_queued: Math.max(0, ...rows.map(r => r.admission?.queued ?? 0)),
          peak_sql_connections: Math.max(0, ...samples),
          queue_full: rows.filter(r => r.admission?.outcome === 'queue_full').length, queue_timeout: rows.filter(r => r.admission?.outcome === 'queue_timeout').length,
          queue_cancelled: rows.filter(r => r.admission?.outcome === 'cancelled').length, deadlines: rows.filter(r => r.deadline).length, response_limits: rows.filter(r => r.response_limit).length }
      }
      case 'stop_engine': await this.stop(); await this.until(() => this.state(), state => state.connections === 0, signal); return true
      case 'start_engine': await this.start(signal); return true
      default: throw new LoadFailure('verification_failed')
    }
  }
  async stop() {
    if (this.child && this.child.exitCode === null && this.child.signalCode === null && !this.engineStartFailed) this.child.kill('SIGKILL')
    if (this.childExited) {
      const timer = new AbortController()
      try { await Promise.race([this.childExited, delay(2000, undefined, { signal: timer.signal }).then(() => { throw new LoadFailure('cleanup_failed') })]) }
      finally { timer.abort() }
    }
    if (this.gateway) { this.gateway.closeAllConnections(); await new Promise(resolve => this.gateway.close(resolve)); this.gateway = null }
  }
  async cleanup(signal) {
    this.allRequests.abort()
    clearInterval(this.sampler)
    let failed = Boolean(this.pendingCreation)
    const attempt = async action => { try { signal.throwIfAborted(); await action() } catch { failed = true } }
    await attempt(() => this.stop())
    if (this.sampling) await attempt(() => this.sampling)
    await attempt(() => Promise.allSettled([...this.inFlight]))
    if (this.owner) { await attempt(() => this.owner.query('ROLLBACK')); await attempt(() => this.owner.end()) }
    if (this.created.database) await attempt(() => this.admin.query(`DROP DATABASE "${this.database}" WITH (FORCE)`))
    if (this.created.role) await attempt(() => this.admin.query(`DROP ROLE "${this.role}"`))
    if (this.admin) await attempt(() => this.admin.end())
    this.tokens.clear()
    return { complete: !failed }
  }
}
