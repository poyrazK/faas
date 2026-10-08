import { generateKeyPairSync, randomBytes, sign } from 'node:crypto'
import { spawn } from 'node:child_process'
import { readFile, writeFile, chmod, mkdir, mkdtemp, cp, rm, rename } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

export class CanaryFailure extends Error {
  constructor(code) { super(code); this.code = code }
}
export const requireValue = (condition, code) => { if (!condition) throw new CanaryFailure(code) }
export function httpsOrigin(value) {
  let url
  try { url = new URL(value) } catch { throw new CanaryFailure('invalid_https_origin') }
  requireValue(url.protocol === 'https:' && !url.username && !url.password && !url.search && !url.hash && url.pathname === '/', 'invalid_https_origin')
  return url.origin
}

export function validateConfig(raw, env) {
  requireValue(raw.environment === 'staging', 'staging_designation_required')
  const api = httpsOrigin(raw.api)
  requireValue(!env.FAAS_API || httpsOrigin(env.FAAS_API) === api, 'staging_api_mismatch')
  requireValue(typeof raw.account_id === 'string' && raw.account_id.length > 0, 'staging_account_required')
  requireValue(typeof raw.region === 'string' && /^[a-z0-9-]+$/.test(raw.region), 'staging_region_required')
  requireValue(Array.isArray(raw.builder_node_ids) && raw.builder_node_ids.length > 0 && raw.builder_node_ids.every(x => typeof x === 'string' && x.length > 0), 'native_builder_allowlist_required')
  requireValue(typeof raw.app_host_suffix === 'string' && /^\.[a-z0-9.-]+$/.test(raw.app_host_suffix), 'staging_app_host_suffix_required')
  requireValue(Boolean(env.FAAS_TOKEN), 'staging_token_required')
  requireValue(/^[a-f0-9]{40}$/.test(env.FAAS_DATA_API_SOURCE_COMMIT || ''), 'source_commit_required')
  return { ...raw, api, token: env.FAAS_TOKEN, sourceCommit: env.FAAS_DATA_API_SOURCE_COMMIT }
}

// Never include argv, output or child errors in evidence: commands and provider
// failures can contain credentials. Operators get stable action names instead.
export function command(binary, args, { cwd, env, timeout = 1_200_000, stdin = '', signal }) {
  return new Promise((resolveResult, reject) => {
    const child = spawn(binary, args, { cwd, env, signal, stdio: ['pipe', 'pipe', 'pipe'] })
    let stdout = '', bytes = 0, rejected = false
    const fail = code => { if (!rejected) { rejected = true; child.kill('SIGKILL'); reject(new CanaryFailure(code)) } }
    const timer = setTimeout(() => fail('command_timeout'), timeout)
    child.on('error', () => { clearTimeout(timer); fail('command_start_failed') })
    child.stdout.on('data', chunk => { bytes += chunk.length; if (bytes > 1_048_576) fail('command_output_limit'); else stdout += chunk })
    child.stderr.on('data', chunk => { bytes += chunk.length; if (bytes > 1_048_576) fail('command_output_limit') })
    child.on('close', code => { clearTimeout(timer); if (!rejected) code === 0 ? resolveResult(stdout) : reject(new CanaryFailure('command_failed')) })
    child.stdin.on('error', () => {})
    child.stdin.end(stdin)
  })
}

export async function apiRequest(config, path, { method = 'GET', body, fetchImpl = fetch, signal } = {}) {
  requireValue(path.startsWith('/v1/') && !path.includes('..'), 'invalid_api_path')
  const response = await fetchImpl(config.api + path, {
    method, redirect: 'error', signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(30_000)]) : AbortSignal.timeout(30_000),
    headers: { Authorization: `Bearer ${config.token}`, Accept: 'application/json', ...(body ? { 'Content-Type': 'application/json' } : {}) },
    body: body ? JSON.stringify(body) : undefined,
  }).catch(() => { throw new CanaryFailure('management_request_failed') })
  requireValue(response.ok, `management_http_${response.status}`)
  if (response.status === 204) return null
  const text = await response.text()
  return text ? JSON.parse(text) : null
}

export class Canary {
  constructor(config, { evidence, cli, sdkTarball, repoRoot, runCommand = command, request, signal } ) {
    Object.assign(this, { config, evidence, cli, sdkTarball, repoRoot, runCommand, signal })
    this.request = request || ((path, options) => apiRequest(config, path, { ...options, signal: this.signal }))
    this.prefix = `dapi-${randomBytes(8).toString('hex')}`
    this.report = { schema_version: 1, scope: 'staging-canary', run_id: this.prefix, source_commit: config.sourceCommit, target: { api: config.api, account_id: config.account_id, region: config.region }, result: 'running', checks: [], resources: [], deployments: [], instances: [], cleanup: { result: 'pending', pending: [] } }
    this.deadline = Date.now() + 60 * 60_000
  }
  async save() {
    await mkdir(dirname(this.evidence), { recursive: true })
    const temporary = this.evidence + '.' + this.prefix + '.tmp'
    await writeFile(temporary, JSON.stringify(this.report, null, 2) + '\n', { mode: 0o600 })
    await chmod(temporary, 0o600)
    await rename(temporary, this.evidence)
  }
  async check(name, action) {
    this.report.current_check = name; await this.save()
    const value = await action()
    this.report.checks.push(name); await this.save()
    return value
  }
  async exec(args, cwd = this.workspace, stdin = '') {
    const stdout = await this.runCommand(this.cli, ['--json', ...args], {
      cwd, stdin, signal: this.signal, timeout: Math.min(1_200_000, Math.max(1, this.deadline - Date.now())),
      env: { ...process.env, FAAS_API: this.config.api, FAAS_TOKEN: this.config.token },
    })
    try { return JSON.parse(stdout) } catch { throw new CanaryFailure('invalid_cli_receipt') }
  }
  async wait(read, ready, timeout = 180_000) {
    const end = Math.min(this.deadline, Date.now() + timeout)
    while (Date.now() < end) {
      requireValue(!this.signal?.aborted, 'canary_interrupted')
      const value = await read()
      if (ready(value)) return value
      requireValue(!['failed', 'cancelled', 'error'].includes(value?.state || value?.status), 'operation_failed')
      await new Promise(r => setTimeout(r, 2000))
    }
    throw new CanaryFailure('operation_timeout')
  }
  async createApp(suffix) {
    const slug = `${this.prefix}-${suffix}`
    const resource = { kind: 'app', slug, state: 'create_pending' }
    this.report.resources.push(resource); await this.save()
    const app = await this.request('/v1/apps', { method: 'POST', body: { slug, type: 'app', require_authn: false } })
    requireValue(app.id && app.slug === slug, 'invalid_app_receipt')
    Object.assign(resource, { id: app.id, state: 'created' }); await this.save()
    await this.request(`/v1/apps/${slug}`, { method: 'PATCH', body: { require_authn: false, public_auth: { mode: 'open' } } })
    return app
  }
  appURL(app) {
    const origin = httpsOrigin(app.url)
    requireValue(new URL(origin).hostname.endsWith(this.config.app_host_suffix), 'unexpected_app_host')
    return origin
  }
  async deployed(receipt, app) {
    requireValue(receipt.id && receipt.status === 'live' && receipt.build_id, 'deployment_not_live')
    const provenance = await this.request(`/v1/builds/${receipt.build_id}/provenance`)
    requireValue(provenance.build_id === receipt.build_id && this.config.builder_node_ids.includes(provenance.builder_node_id) && /^[a-f0-9]{64}$/.test(provenance.source_sha256) && provenance.source_sha256 === receipt.source_sha256, 'native_build_provenance_missing')
    this.report.deployments.push({ app: app.slug, deployment_id: receipt.id, build_id: receipt.build_id, builder_node_id: provenance.builder_node_id, source_sha256: provenance.source_sha256 })
    await this.save()
  }
  async deploy(app, directory) {
    const receipt = await this.exec(['deploy', '--name', app.slug, '--dockerfile', '--source', 'worktree', '--healthcheck-path', '/healthz', '--no-doctor', '--no-require-authn'], directory)
    await this.deployed(receipt, app)
  }
  async attachMigration(database, app) {
    const binding = await this.exec(['postgres', 'attach', database, app.slug, '--scope', 'default', '--access', 'migration', '--env', 'MIGRATION_DATABASE_URL'])
    requireValue(binding.id && binding.app_id === app.id && binding.access === 'migration', 'invalid_migration_binding_receipt')
    await this.wait(() => this.exec(['postgres', 'bindings', 'get', binding.id]), x => x.state === 'ready')
  }
  async refresh(app) {
    const receipt = await this.exec(['data-api', 'refresh', app.slug, '--wait', '--timeout', '5m'])
    requireValue(receipt.wake_id, 'restart_receipt_missing')
    requireValue(receipt.status === 'completed' && receipt.ready === true, 'schema_refresh_not_ready')
  }
  publicFetch(input, init = {}) {
    return fetch(input, { ...init, redirect: 'error', signal: this.signal ? AbortSignal.any([this.signal, AbortSignal.timeout(30_000)]) : AbortSignal.timeout(30_000) })
  }
  async cleanup() {
    this.signal = undefined
    this.deadline = Date.now() + 10 * 60_000
    const pending = []
    const ownedApps = new Set(this.report.resources.filter(x => x.kind === 'app' && x.id).map(x => x.id))
    const blockedDatabases = new Set()
    // Retire serving and migration bindings before deleting their parent apps
    // or provider database. Revocation has its own asynchronous saga.
    for (const r of this.report.resources.filter(x => x.kind === 'database' && x.id)) {
      try {
        const bindings = await this.exec(['postgres', 'bindings', 'list', r.id])
        requireValue(Array.isArray(bindings.items) && bindings.items.every(x => ownedApps.has(x.app_id)), 'cleanup_binding_identity_mismatch')
        for (const binding of bindings.items) {
          if (binding.state === 'deleted') continue
          await this.exec(['postgres', 'bindings', 'delete', binding.id])
          await this.wait(() => this.exec(['postgres', 'bindings', 'get', binding.id]), x => x.state === 'deleted', 180_000)
        }
      } catch {
        blockedDatabases.add(r.id)
        pending.push({ kind: 'database', id: r.id, reason: 'binding_cleanup_failed' })
      }
    }
    // Delete only IDs acknowledged by this run. Unknown creation outcomes are
    // surfaced for reconciliation, never adopted by a matching display name.
    for (const r of [...this.report.resources].reverse()) {
      if (!r.id) { pending.push({ kind: r.kind, slug: r.slug, name: r.name, reason: 'create_outcome_unknown' }); continue }
      if (blockedDatabases.has(r.id)) continue
      try {
        if (r.kind === 'app') {
          const app = await this.request(`/v1/apps/${r.slug}`)
          requireValue(app.id === r.id, 'cleanup_identity_mismatch')
          await this.request(`/v1/apps/${r.slug}`, { method: 'DELETE' })
        } else {
          await this.exec(['postgres', 'delete', r.id])
          await this.wait(() => this.exec(['postgres', 'get', r.id]), x => x.state === 'deleted', 300_000)
        }
        r.state = 'deleted'
      } catch { pending.push({ kind: r.kind, id: r.id, slug: r.slug, reason: 'cleanup_failed' }) }
    }
    this.report.cleanup = { result: pending.length ? 'failed' : 'passed', pending }
    if (pending.length) this.report.result = 'failed'
    await this.save()
  }
  async run() {
    this.workspace = await mkdtemp(join(tmpdir(), 'gregale-data-api-canary-'))
    try {
      await this.save()
      await this.check('staging_account', async () => requireValue((await this.request('/v1/account')).id === this.config.account_id, 'staging_account_mismatch'))
      const dbResource = { kind: 'database', name: this.prefix, state: 'create_pending' }
      this.report.resources.push(dbResource); await this.save()
      const db = await this.check('database_ready', async () => {
        const created = await this.exec(['postgres', 'create', this.prefix, '--region', this.config.region])
        requireValue(created.id && created.name === this.prefix, 'invalid_database_receipt')
        Object.assign(dbResource, { id: created.id, state: 'created' }); await this.save()
        return this.wait(() => this.exec(['postgres', 'get', created.id]), x => x.state === 'ready', 600_000)
      })
      const issuer = await this.createApp('issuer')
      const issuerURL = this.appURL(issuer)
      const { privateKey, publicKey } = generateKeyPairSync('ec', { namedCurve: 'P-256' })
      const jwk = { ...publicKey.export({ format: 'jwk' }), kid: this.prefix, alg: 'ES256', use: 'sig' }
      const jwt = (sub, audience = this.prefix) => {
        const enc = value => Buffer.from(JSON.stringify(value)).toString('base64url')
        const now = Math.floor(Date.now() / 1000)
        const data = `${enc({ alg: 'ES256', kid: this.prefix })}.${enc({ sub, iss: issuerURL, aud: audience, exp: now + 300, iat: now, role: 'postgres' })}`
        return `${data}.${sign('sha256', Buffer.from(data), { key: privateKey, dsaEncoding: 'ieee-p1363' }).toString('base64url')}`
      }
      const issuerDir = join(this.workspace, 'issuer'); await mkdir(issuerDir)
      await cp(join(this.repoRoot, 'tests/data-api/staging/issuer.mjs'), join(issuerDir, 'issuer.mjs'))
      await writeFile(join(issuerDir, 'jwks.json'), JSON.stringify({ keys: [jwk] }))
      await writeFile(join(issuerDir, 'Dockerfile'), 'FROM node:22-bookworm-slim\nWORKDIR /app\nCOPY . .\nEXPOSE 8080\nCMD ["node","issuer.mjs"]\n')
      await this.check('issuer_native_build', () => this.deploy(issuer, issuerDir))
      const migrator = await this.createApp('migrate')
      await this.check('migration_binding_ready', () => this.attachMigration(db.id, migrator))
      await this.request(`/v1/apps/${migrator.slug}`, { method: 'PATCH', body: { egress_ports: [5432] } })
      const migrationDir = join(this.workspace, 'migration'); await mkdir(migrationDir)
      for (const file of ['package.json', 'package-lock.json']) await cp(join(this.repoRoot, 'cmd/gregale/templates/data-api', file), join(migrationDir, file))
      for (const file of ['migrate.mjs', 'migration-server.mjs']) await cp(join(this.repoRoot, 'tests/data-api/staging', file), join(migrationDir, file))
      await writeFile(join(migrationDir, 'version'), '1')
      await writeFile(join(migrationDir, 'gregale.yaml'), 'release:\n  command: node migrate.mjs\n')
      await writeFile(join(migrationDir, 'Dockerfile'), 'FROM node:22-bookworm-slim\nWORKDIR /app\nCOPY package*.json ./\nRUN npm ci --omit=dev --ignore-scripts\nCOPY . .\nEXPOSE 8080\nCMD ["node","migration-server.mjs"]\n')
      await this.check('migration_release_native_build', () => this.deploy(migrator, migrationDir))
      await this.check('migration_credential_release_only', async () => {
        const response = await this.publicFetch(this.appURL(migrator) + '/healthz')
        requireValue(response.ok && (await response.json()).migration_credential_present === false, 'migration_runtime_custody_failed')
      })
      const app = await this.createApp('api')
      await this.check('data_api_native_build', async () => this.deployed(await this.exec(['data-api', 'create', app.slug, '--database', db.id, '--issuer', issuerURL, '--jwks-url', issuerURL + '/jwks.json', '--audience', this.prefix, '--resume']), app))
      const url = this.appURL(app)
      const clientDir = join(this.workspace, 'client'); await mkdir(clientDir)
      await writeFile(join(clientDir, 'package.json'), '{"type":"module"}')
      await this.runCommand('npm', ['install', '--ignore-scripts', '--no-audit', '--no-fund', this.sdkTarball], { cwd: clientDir, env: process.env, signal: this.signal })
      await cp(join(this.repoRoot, 'tests/data-api/staging/client.ts'), join(clientDir, 'client.ts'))
      await this.exec(['data-api', 'types', app.slug, '--output', join(clientDir, 'database.types.ts')])
      await this.exec(['data-api', 'types', app.slug, '--output', join(clientDir, 'database.types.ts'), '--check'])
      const compile = () => this.runCommand(join(this.repoRoot, 'sdk/data/node_modules/.bin/tsc'), ['--strict', '--skipLibCheck', '--target', 'ES2022', '--module', 'NodeNext', '--outDir', 'dist', 'client.ts'], { cwd: clientDir, env: process.env, signal: this.signal })
      await this.check('generated_types_editor_contract', compile)
      const client = await import(pathToFileURL(join(clientDir, 'dist/client.js')))
      const clientFetch = (input, init) => this.publicFetch(input, init)
      const id = await this.check('typed_crud_and_subject_isolation', () => client.exercise(url, jwt('alice'), jwt('bob'), clientFetch))
      const { createDataClient } = await import(pathToFileURL(join(clientDir, 'node_modules/@gregale/data/dist/index.js')))
      const alice = createDataClient({ url, accessToken: () => jwt('alice'), fetch: clientFetch }).schema('api')
      await this.check('jwt_audience_and_openapi', async () => {
        const bad = await this.publicFetch(url + '/rest/v1/notes', { headers: { Authorization: `Bearer ${jwt('alice', 'wrong')}` } })
        requireValue(bad.status === 401, 'wrong_audience_accepted')
        const openapi = await this.publicFetch(url + '/openapi.json', { headers: { Authorization: `Bearer ${jwt('alice')}` } })
        requireValue(openapi.ok && (await openapi.json()).paths?.['/notes'], 'public_openapi_missing')
      })
      await this.check('park_and_authenticated_wake', async () => {
        const deployment = this.report.deployments.find(x => x.app === app.slug).deployment_id
        const parked = await this.wait(() => this.request(`/v1/apps/${app.slug}/instances`), xs => xs.some(x => x.state === 'PARKED' && x.deployment_id === deployment), 900_000)
        const record = instances => instances.filter(x => x.deployment_id === deployment && ['PARKED', 'RUNNING'].includes(x.state)).map(x => ({ id: x.id, deployment_id: x.deployment_id, wake_id: x.wake_id, state: x.state }))
        this.report.instances.push(...record(parked)); await this.save()
        const start = Date.now()
        const result = await alice.from('notes').select('id,body').eq('id', id).single()
        requireValue(!result.error && result.data.body === 'updated', 'authenticated_wake_failed')
        this.report.wake_duration_ms = Date.now() - start
        const running = await this.wait(() => this.request(`/v1/apps/${app.slug}/instances`), xs => xs.some(x => x.state === 'RUNNING' && x.deployment_id === deployment))
        this.report.instances.push(...record(running))
      })
      await writeFile(join(migrationDir, 'version'), '2')
      await this.check('schema_migration_native_build', () => this.deploy(migrator, migrationDir))
      await this.check('schema_refresh_completion', () => this.refresh(app))
      await this.exec(['data-api', 'types', app.slug, '--output', join(clientDir, 'database.types.ts')])
      await this.check('migrated_type_and_rest_contract', async () => {
        const types = await readFile(join(clientDir, 'database.types.ts'), 'utf8')
        requireValue(types.includes('"priority"'), 'migrated_type_missing')
        await compile()
        await writeFile(join(clientDir, 'priority.ts'), 'import {createDataClient} from "@gregale/data"; import type {Database} from "./database.types.js"; const db=createDataClient<Database>({url:"https://unused.example",accessToken:"unused"}).schema("api"); db.from("notes").insert({subject:"alice",body:"new",priority:1});')
        await this.runCommand(join(this.repoRoot, 'sdk/data/node_modules/.bin/tsc'), ['--strict', '--skipLibCheck', '--noEmit', '--target', 'ES2022', '--module', 'NodeNext', 'priority.ts'], { cwd: clientDir, env: process.env, signal: this.signal })
        const result = await alice.from('notes').select('priority').eq('id', id).single()
        requireValue(!result.error && result.data.priority === 0, 'migrated_rest_contract_missing')
      })
      await this.check('credential_rotation_preserves_data', async () => {
        const bindings = await this.exec(['postgres', 'bindings', 'list', db.id])
        const binding = bindings.items.filter(x => x.app_id === app.id && x.access === 'data_api')
        requireValue(binding.length === 1, 'data_api_binding_missing')
        await this.exec(['postgres', 'bindings', 'rotate', binding[0].id, '--wait', '--wait-timeout', '5m'])
        await this.refresh(app)
        const result = await alice.from('notes').select('id,body,priority').eq('id', id).single()
        requireValue(!result.error && result.data.body === 'updated' && result.data.priority === 0, 'rotated_credential_data_lost')
        const bob = createDataClient({ url, accessToken: () => jwt('bob'), fetch: clientFetch }).schema('api')
        const hidden = await bob.from('notes').select('id').eq('id', id)
        requireValue(!hidden.error && hidden.data.length === 0, 'rotated_credential_rls_lost')
        const deleted = await alice.from('notes').delete().eq('id', id).select('id')
        requireValue(!deleted.error && deleted.data.length === 1, 'typed_delete_failed')
      })
      this.report.result = 'passed'
    } catch (error) {
      this.report.result = 'failed'
      // All unexpected library/provider messages are reduced to this code.
      this.report.failure = { check: this.report.current_check, code: this.signal?.aborted ? 'canary_interrupted' : error instanceof CanaryFailure ? error.code : 'canary_check_failed' }
    } finally {
      await this.cleanup()
      await rm(this.workspace, { recursive: true, force: true })
    }
    requireValue(this.report.result === 'passed', 'staging_canary_failed')
    return this.report
  }
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  try {
    const config = validateConfig(JSON.parse(await readFile(process.env.FAAS_DATA_API_STAGING_CONFIG, 'utf8')), process.env)
    if (process.argv.includes('--check-config')) console.log('Data API staging configuration passed; no provider calls made')
    else {
      const interrupt = new AbortController()
      process.once('SIGINT', () => interrupt.abort())
      process.once('SIGTERM', () => interrupt.abort())
      const runner = new Canary(config, { evidence: resolve(process.env.FAAS_DATA_API_EVIDENCE || 'data-api-staging-evidence.json'), cli: process.env.GREGALE_CANARY_BIN, sdkTarball: process.env.FAAS_DATA_API_SDK_TARBALL, repoRoot: resolve(new URL('../../../', import.meta.url).pathname), signal: interrupt.signal })
      const report = await runner.run()
      console.log(`Data API staging canary passed ${report.checks.length} checks; cleanup passed`)
    }
  } catch (error) {
    const code = error instanceof CanaryFailure ? error.code : 'configuration_or_canary_failure'
    console.error(`Data API staging canary failed (${code}); inspect configuration and evidence when present`)
    process.exitCode = 1
  }
}
