import { createHash, generateKeyPairSync, randomBytes, sign } from 'node:crypto'
import { spawn } from 'node:child_process'
import { readFile, writeFile, chmod, mkdir, mkdtemp, cp, rm, rename, lstat, realpath } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
import { parseArgs } from 'node:util'

import { captureContract, qualifyRollout } from './rollout.mjs'
import { CanaryFailure, requireValue } from './checks.mjs'
import { verifiedSync, verifiedContract, matchingRuntimeLog, verifiedDiagnostic } from './workflow.mjs'
import { qualifyHostedLoad } from './load.mjs'
import { options as loadOptions } from '../load/contracts.mjs'
import { setTimeout as delay } from 'node:timers/promises'
import { readinessPath } from '../../../cmd/gregale/templates/data-api/config.mjs'
import { validateBundle, verifyFile } from '../../../cmd/gregale/templates/data-api-starter/tools/artifact-lib.mjs'
export { CanaryFailure, requireValue } from './checks.mjs'

export function httpsOrigin(value) {
  let url
  try { url = new URL(value) } catch { throw new CanaryFailure('invalid_https_origin') }
  requireValue(url.protocol === 'https:' && !url.username && !url.password && !url.search && !url.hash && url.pathname === '/', 'invalid_https_origin')
  return url.origin
}

const validRuntimeVersion = value => typeof value === 'string' && value.length === 16 && /^[a-f0-9]+$/.test(value)

function localToolEnv() {
  const env = { ...process.env }
  for (const name of ['FAAS_TOKEN', 'GREGALE_CONNECT_ALICE', 'GREGALE_CONNECT_BOB', 'GREGALE_DATA_API_ACCESS_TOKEN', 'GREGALE_DATA_API_REFRESH_TOKEN', 'GREGALE_ARTIFACT_TOKEN', 'STAGING_CONFIG_JSON']) delete env[name]
  return env
}

export function validateConfig(raw, env, { checkConfigOnly = false, mode = 'full' } = {}) {
  requireValue(mode === 'full' || mode === 'smoke', 'invalid_canary_mode')
  requireValue(raw.connect === undefined || typeof raw.connect === 'boolean', 'invalid_connect_option')
  requireValue(raw.write_inspection === undefined || typeof raw.write_inspection === 'boolean', 'invalid_write_inspection_option')
  requireValue(raw.environment === 'staging', 'staging_designation_required')
  const api = httpsOrigin(raw.api)
  requireValue(!env.FAAS_API || httpsOrigin(env.FAAS_API) === api, 'staging_api_mismatch')
  requireValue(typeof raw.account_id === 'string' && raw.account_id.length > 0, 'staging_account_required')
  requireValue(typeof raw.region === 'string' && /^[a-z0-9-]+$/.test(raw.region), 'staging_region_required')
  requireValue(Array.isArray(raw.builder_node_ids) && raw.builder_node_ids.length > 0 && raw.builder_node_ids.every(x => typeof x === 'string' && x.length > 0), 'native_builder_allowlist_required')
  requireValue(typeof raw.app_host_suffix === 'string' && /^\.[a-z0-9.-]+$/.test(raw.app_host_suffix), 'staging_app_host_suffix_required')
  requireValue(Boolean(env.FAAS_TOKEN), 'staging_token_required')
  requireValue(/^[a-f0-9]{40}$/.test(env.FAAS_DATA_API_SOURCE_COMMIT || ''), 'source_commit_required')
  const expectedRuntimeVersion = env.FAAS_DATA_API_EXPECTED_RUNTIME_VERSION
  requireValue(checkConfigOnly || validRuntimeVersion(expectedRuntimeVersion), 'expected_runtime_version_required')
  let load
  try { load = loadOptions(raw.load) } catch { throw new CanaryFailure('invalid_load_options') }
  return { ...raw, api, load, token: env.FAAS_TOKEN, sourceCommit: env.FAAS_DATA_API_SOURCE_COMMIT, expectedRuntimeVersion, mode }
}

// Never include argv, output or child errors in evidence: commands and provider
// failures can contain credentials. Operators get stable action names instead.
export function command(binary, args, { cwd, env, timeout = 1_200_000, stdin = '', signal, expectedExitCode = 0, expectedStderrIncludes = '' }) {
  return new Promise((resolveResult, reject) => {
    const child = spawn(binary, args, { cwd, env, signal, stdio: ['pipe', 'pipe', 'pipe'] })
    let stdout = '', stderr = '', bytes = 0, rejected = false
    const fail = code => { if (!rejected) { rejected = true; child.kill('SIGKILL'); reject(new CanaryFailure(code)) } }
    const timer = setTimeout(() => fail('command_timeout'), timeout)
    child.on('error', () => { clearTimeout(timer); fail('command_start_failed') })
    child.stdout.on('data', chunk => { bytes += chunk.length; if (bytes > 1_048_576) fail('command_output_limit'); else stdout += chunk })
    child.stderr.on('data', chunk => { bytes += chunk.length; if (bytes > 1_048_576) fail('command_output_limit'); else if (expectedStderrIncludes) stderr += chunk })
    child.on('close', code => {
      clearTimeout(timer)
      if (!rejected) code === expectedExitCode && (!expectedStderrIncludes || stderr.includes(expectedStderrIncludes)) ? resolveResult(stdout) : reject(new CanaryFailure('command_failed'))
    })
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
  constructor(config, { evidence, cli, sdkTarball, sdkBundle, repoRoot, runCommand = command, request, signal, loadModule = path => import(pathToFileURL(path)) } ) {
    Object.assign(this, { config, evidence, cli, sdkTarball, sdkBundle, repoRoot, runCommand, signal, loadModule })
    this.request = request || ((path, options) => apiRequest(config, path, { ...options, signal: options?.signal && this.signal ? AbortSignal.any([options.signal, this.signal]) : options?.signal ?? this.signal }))
    this.prefix = `dapi-${randomBytes(8).toString('hex')}`
    this.report = { schema_version: 1, scope: 'staging-canary', run_id: this.prefix, source_commit: config.sourceCommit, target: { api: config.api, account_id: config.account_id, region: config.region }, result: 'running', checks: [], resources: [], deployments: [], instances: [], syncs: [], requests: [], cleanup: { result: 'pending', pending: [] } }
    this.report.runtime = { candidate_version: config.expectedRuntimeVersion, control_plane_version: null, serving_version: null }
    this.report.mode = config.mode
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
  async verifiedHostedBundle() {
    try {
      requireValue(typeof this.sdkBundle === 'string' && this.sdkBundle.length > 0 && typeof this.sdkTarball === 'string' && this.sdkTarball.length > 0, 'hosted_bundle_required')
      const info = await lstat(this.sdkBundle)
      requireValue(info.isFile() && info.size <= 65536, 'hosted_bundle_invalid')
      const bundle = validateBundle(JSON.parse(await readFile(this.sdkBundle, 'utf8')))
      requireValue(bundle.source.commit === this.config.sourceCommit, 'hosted_bundle_source_mismatch')
      const archive = join(dirname(this.sdkBundle), bundle.sdk.file)
      requireValue(await realpath(archive) === await realpath(this.sdkTarball), 'hosted_bundle_sdk_mismatch')
      await verifyFile(archive, bundle.sdk)
      return this.sdkBundle
    } catch (error) {
      if (error instanceof CanaryFailure) throw error
      throw new CanaryFailure('hosted_bundle_invalid')
    }
  }
  async freezeHostedBundle() {
    const source = await this.verifiedHostedBundle()
    const metadata = await readFile(source)
    const bundle = validateBundle(JSON.parse(metadata))
    const directory = join(this.workspace, 'approved-sdk')
    await mkdir(directory)
    const manifest = join(directory, 'data-api-bundle.json')
    const archive = join(directory, bundle.sdk.file)
    await writeFile(manifest, metadata, { mode: 0o600, flag: 'wx' })
    await cp(this.sdkTarball, archive, { force: false, errorOnExist: true })
    this.sdkBundle = manifest
    this.sdkTarball = archive
    await this.verifiedHostedBundle()
  }
  async exec(args, cwd = this.workspace, stdin = '', applicationToken, signal = this.signal) {
    const stdout = await this.runCommand(this.cli, ['--json', ...args], {
      cwd, stdin, signal, timeout: Math.min(1_200_000, Math.max(1, this.deadline - Date.now())),
      env: { ...process.env, FAAS_API: this.config.api, FAAS_TOKEN: this.config.token, ...(applicationToken ? { GREGALE_DATA_API_ACCESS_TOKEN: applicationToken } : {}) },
    })
    try { return JSON.parse(stdout) } catch { throw new CanaryFailure('invalid_cli_receipt') }
  }
  async wait(read, ready, timeout = 180_000, signal = this.signal) {
    const end = Math.min(this.deadline, Date.now() + timeout)
    while (Date.now() < end) {
      requireValue(!signal?.aborted, 'canary_interrupted')
      const value = await read()
      if (ready(value)) return value
      requireValue(!['failed', 'cancelled', 'error'].includes(value?.state || value?.status), 'operation_failed')
      await delay(2000, undefined, { signal })
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
    const settings = { require_authn: false, public_auth: { mode: 'open' } }
    // The migration fixture serves its custody check on /healthz.
    if (suffix === 'migrate') settings.health_path = '/__gregale/canary-edge-health'
    await this.request(`/v1/apps/${slug}`, { method: 'PATCH', body: settings })
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
  async qualifyWriteInspection({ db, app }) {
    const bindings = await this.exec(['postgres', 'bindings', 'list', db.id])
    requireValue(Array.isArray(bindings.items), 'inspection_binding_missing')
    const matches = bindings.items.filter(x => x.database_id === db.id && x.app_id === app.id && x.access === 'data_api' && x.scope === 'default' && x.environment_key === 'DATABASE_URL' && x.state === 'ready' && x.rotation_pending === false)
    requireValue(matches.length === 1 && typeof matches[0].id === 'string' && matches[0].id && Number.isSafeInteger(matches[0].credential_generation) && matches[0].credential_generation > 0, 'inspection_binding_missing')
    const deployment = this.report.deployments.find(x => x.app === app.slug)
    requireValue(typeof deployment?.deployment_id === 'string' && deployment.deployment_id, 'inspection_deployment_missing')
    const stdout = await this.runCommand(this.cli, ['--json', 'data-api', 'write-readiness', '--database', db.id, '--app', app.slug, '--binding', matches[0].id, '--schema', 'api', '--table', 'notes', '--operations', 'insert'], {
      cwd: this.workspace, signal: this.signal, expectedExitCode: 1,
      timeout: Math.min(240_000, Math.max(1, this.deadline - Date.now())),
      env: { ...process.env, FAAS_API: this.config.api, FAAS_TOKEN: this.config.token },
    })
    let report
    try { report = JSON.parse(stdout) } catch { throw new CanaryFailure('inspection_report_invalid') }
    const identityMatches = report?.app_id === app.id && report.database_id === db.id && report.binding_id === matches[0].id && report.deployment_id === deployment.deployment_id && report.credential_generation === matches[0].credential_generation
    const scopeMatches = report?.scope?.schema === 'api' && report.scope.table === 'notes' && Array.isArray(report.scope.operations) && report.scope.operations.length === 1 && report.scope.operations[0] === 'insert'
    const verified = report?.binding_identity?.status === 'verified' && report.binding_identity.reason === 'provider_target_and_trust_matched'
    requireValue(report?.version === 1 && report.status === 'review_required' && report.authorization === 'not_tested' && report.binding_delivery === 'not_independently_verified' && typeof report.task_id === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(report.task_id) && identityMatches && scopeMatches && verified, 'inspection_report_invalid')
    this.report.write_inspection = { task_id: report.task_id, deployment_id: report.deployment_id, binding_id: report.binding_id, credential_generation: report.credential_generation, binding_identity: 'verified', authorization: 'not_tested' }
    await this.save()
  }
  async refresh(app, signal = this.signal) {
    const receipt = await this.exec(['data-api', 'refresh', app.slug, '--wait', '--timeout', '5m'], this.workspace, '', undefined, signal)
    requireValue(receipt.wake_id, 'restart_receipt_missing')
    requireValue(receipt.status === 'completed' && receipt.ready === true, 'schema_refresh_not_ready')
    return receipt
  }
  publicFetch(input, init = {}) {
    return fetch(input, { ...init, redirect: 'error', signal: AbortSignal.any([this.signal, init.signal, AbortSignal.timeout(30_000)].filter(Boolean)) })
  }
  async checkControlPlaneRuntime(app) {
    const status = await this.request(`/v1/apps/${app.slug}/data-api/status`)
    const version = status?.runtime_version
    requireValue(validRuntimeVersion(version), 'control_plane_runtime_version_missing')
    this.report.runtime.control_plane_version = version
    requireValue(version === this.config.expectedRuntimeVersion, 'control_plane_runtime_version_mismatch')
  }
  async checkServingRuntime(url) {
    const response = await this.publicFetch(url + readinessPath, { headers: { Accept: 'application/json', 'Cache-Control': 'no-cache' } })
    requireValue(response.status === 200 && response.headers.get('x-faas-health-source') !== 'edge', 'serving_runtime_health_unavailable')
    const health = await response.json().catch(() => null)
    requireValue(health?.version === 1 && health.live === true && health.ready === true, 'serving_runtime_health_invalid')
    requireValue(validRuntimeVersion(health.runtime_version), 'serving_runtime_version_missing')
    this.report.runtime.serving_version = health.runtime_version
    requireValue(health.runtime_version === this.config.expectedRuntimeVersion, 'serving_runtime_version_mismatch')
  }
  async checkPublicCache(app, token) {
    await this.exec(['data-api', 'configure', app.slug, '--public-read', 'api.smoke_public_catalog', '--public-read-cache', '60s', '--no-restart'])
    await this.refresh(app)
    const current = await this.request(`/v1/apps/${app.slug}`)
    requireValue(current.id === app.id, 'cache_app_identity_changed')
    const url = this.appURL({ url: current.canonical_url || current.url })
    await this.contract(url, token, this.report.syncs.at(-1).fingerprint)
    const path = url + '/rest/v1/smoke_public_catalog?select=id%2Cname'
    const requestID = response => {
      const id = response.headers.get('x-faas-request-id')
      requireValue(typeof id === 'string' && (id.length === 32 || id.length === 36) && /^(?:[a-f0-9]{32}|[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12})$/i.test(id), 'public_cache_request_id_missing')
      return id
    }
    const json = async () => {
      const response = await this.publicFetch(path, { headers: { Accept: 'application/json', 'Accept-Profile': 'api' } })
      requireValue(response.status === 200 && /^application\/json\b/i.test(response.headers.get('content-type') || ''), 'public_cache_json_invalid')
      const data = await response.json().catch(() => null)
      requireValue(Array.isArray(data) && data.length === 1 && data[0].id === 1 && data[0].name === 'canary', 'public_cache_json_invalid')
      return response.headers.get('x-faas-cache') === 'hit' ? requestID(response) : null
    }
    await json()
    const jsonRequestID = await this.wait(json, id => id !== null, 30_000)
    const csv = await this.publicFetch(path, { headers: { Accept: 'text/csv', 'Accept-Profile': 'api' } })
    requireValue(csv.status === 200 && /^text\/csv\b/i.test(csv.headers.get('content-type') || '') && !csv.headers.has('x-faas-cache'), 'public_cache_csv_invalid')
    requireValue((await csv.text()).trim() === 'id,name\n1,canary', 'public_cache_csv_invalid')
    const csvRequestID = requestID(csv)
    requireValue(csvRequestID.toLowerCase() !== jsonRequestID.toLowerCase(), 'public_cache_request_id_reused')
    this.report.public_cache = { json_hit: true, csv_verified: true, json_request_id: jsonRequestID, csv_request_id: csvRequestID, gateway_attribution: 'unavailable' }
  }
  async sync(app, migrator, migrationDir, clientDir, applicationToken) {
    const compiler = join(this.repoRoot, 'sdk/data/node_modules/.bin/tsc')
    const smoke = this.config.mode === 'smoke'
    const workflow = {
      output: 'database.types.ts',
      ...(smoke ? { numeric_mode: 'exact' } : {}),
      migrate: { directory: migrationDir, command: [this.cli, 'deploy', '--name', migrator.slug, '--dockerfile', '--source', 'worktree', '--wait', '--timeout', '15m', '--healthcheck-path', '/healthz', '--no-doctor', '--no-require-authn'] },
      check: { directory: '.', command: [compiler, '--strict', '--target', 'ES2022', '--module', 'NodeNext', '--outDir', 'dist', 'client.ts', smoke ? 'smoke-client.ts' : 'workflow-client.ts'] },
    }
    if ((await readFile(join(migrationDir, 'version'), 'utf8')).trim() === '2') {
      await writeFile(join(clientDir, 'priority.ts'), 'import {createDataClient} from "@gregale/data"; import type {Database} from "./database.types.js"; const db=createDataClient<Database>({url:"https://unused.example",accessToken:"unused"}).schema("api"); db.from("notes").insert({subject:"alice",body:"new",priority:1});')
      workflow.check.command.push('priority.ts')
    }
    const configPath = join(clientDir, 'data-api.json')
    await writeFile(configPath, JSON.stringify(workflow))
    const receipt = await this.exec(['data-api', 'sync', app.slug, '--config', configPath, '--timeout', '20m'], clientDir, '', applicationToken)
    const verified = verifiedSync(receipt, app, await readFile(join(clientDir, 'database.types.ts'), 'utf8'))
    requireValue(this.report.deployments.some(x => x.app === app.slug && x.deployment_id === verified.deployment_id), 'sync_serving_deployment_changed')
    this.report.syncs.push(verified); await this.save()
    const history = await this.request(`/v1/apps/${migrator.slug}/deployments`)
    const fresh = history.items?.filter(x => x.status === 'live' && !this.report.deployments.some(saved => saved.deployment_id === x.id))
    requireValue(fresh?.length === 1, 'sync_migration_deployment_ambiguous')
    await this.deployed(await this.request(`/v1/deployments/${fresh[0].id}`), migrator)
    return verified
  }
  captureContract(...args) { return captureContract(this, ...args) }
  qualifyRollout(options) { return qualifyRollout(this, options) }
  qualifyLoad(options) { return qualifyHostedLoad(this, options) }
  async contract(url, token, expected, signal = this.signal) {
    const response = await this.publicFetch(url + '/__gregale/schema', { signal, headers: { Authorization: `Bearer ${token}` } })
    requireValue(response.ok, 'serving_contract_unavailable')
    verifiedContract(await response.json(), expected)
  }
  async correlate(app, info, method, route) {
    // Logs are NDJSON envelopes, not a single CLI receipt. Keep raw lines out
    // of evidence, and query only a newly observed, validated request UUID.
    verifiedDiagnostic(info)
    const deployment = this.report.deployments.find(x => x.app === app.slug)?.deployment_id
    const record = await this.wait(async () => {
      const output = await this.runCommand(this.cli, ['--json', 'logs', app.slug, '--source', 'runtime', '--grep', info.requestId], {
        cwd: this.workspace, env: { ...process.env, FAAS_API: this.config.api, FAAS_TOKEN: this.config.token }, signal: this.signal, timeout: 30_000,
      })
      return matchingRuntimeLog(output, info, deployment, method, route)
    }, value => value !== null, 30_000)
    this.report.requests.push(record); await this.save()
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
  async qualifyConnect({ db, app, issuerURL, jwt }) {
    const slug = `${this.prefix}-connect`
    const receipt = join(this.workspace, 'connect-receipt.json')
    const output = join(this.workspace, 'connect.types.ts')
    const collection = join(this.workspace, 'connect.requests.json')
    const matrix = join(this.workspace, 'connect.matrix.json')
    const identities = join(this.workspace, 'connect.identities.json')
    const progress = async stage => { this.report.current_check = `connected_${stage}`; await this.save() }
    await writeFile(matrix, JSON.stringify({ version: 1, status: 'reviewed', relation: 'connect_canary_notes', key_column: 'id', identities: ['alice', 'bob'], fixtures: [
      {name: 'alice_note', key: 1, visible_to: ['alice']},
      {name: 'bob_note', key: 2, visible_to: ['bob']},
    ] }), { mode: 0o600 })
    await writeFile(identities, JSON.stringify({ alice: 'GREGALE_CONNECT_ALICE', bob: 'GREGALE_CONNECT_BOB' }), { mode: 0o600 })
    const run = (args, expectation = {}, onlyAlice = false) => {
      const childEnv = { ...process.env, FAAS_API: this.config.api, FAAS_TOKEN: this.config.token, GREGALE_CONNECT_ALICE: jwt('alice', this.prefix, 1800), npm_config_offline: 'true' }
      for (const name of ['STAGING_CONFIG_JSON', 'GREGALE_CONNECT_BOB', 'GREGALE_DATA_API_ACCESS_TOKEN', 'GREGALE_DATA_API_REFRESH_TOKEN', 'GREGALE_ARTIFACT_TOKEN']) delete childEnv[name]
      if (!onlyAlice) childEnv.GREGALE_CONNECT_BOB = jwt('bob', this.prefix, 1800)
      return this.runCommand(this.cli, args, { cwd: this.workspace, signal: this.signal, timeout: Math.min(1_200_000, Math.max(1, this.deadline - Date.now())), env: childEnv, ...expectation })
    }
    await run(['data-api', 'connect', slug, '--prepare-checks', matrix, '--collection', collection])
    await run(['data-api', 'connect', slug, '--database', db.id])
    const resource = { kind: 'app', slug, state: 'create_pending' }
    this.report.resources.push(resource); await this.save()
    try {
      await run(['data-api', 'connect', slug, '--database', db.id, '--apply', '--issuer', issuerURL, '--jwks-url', issuerURL + '/jwks.json', '--audience', this.prefix, '--output', output, '--collection', collection, '--identities', identities, '--receipt', receipt])
    } finally {
      // Only a creation response acknowledged and persisted by the CLI may
      // authorize cleanup. An unknown outcome remains pending for an operator.
      try {
        const saved = JSON.parse(await readFile(receipt, 'utf8'))
        if (saved.version === 1 && saved.app === slug && saved.database_id === db.id && typeof saved.app_id === 'string' && saved.app_id) Object.assign(resource, { id: saved.app_id, state: 'created' })
      } catch {}
      await this.save()
    }
    requireValue(resource.id, 'connect_app_receipt_missing')
    const saved = JSON.parse(await readFile(receipt, 'utf8'))
    requireValue(saved.stage === 'verified' && saved.deployment_id, 'connect_verification_incomplete')
    const connectedApp = await this.request(`/v1/apps/${slug}`)
    requireValue(connectedApp.id === resource.id, 'connect_app_identity_mismatch')
    await this.deployed(await this.request(`/v1/deployments/${saved.deployment_id}`), connectedApp)
    await this.checkControlPlaneRuntime(connectedApp)
    await this.checkServingRuntime(this.appURL(connectedApp))
    await run(['data-api', 'connect', slug, '--verify', '--collection', collection, '--identities', identities, '--receipt', receipt])
    await run(['data-api', 'connect', slug, '--resume', '--collection', collection, '--identities', identities, '--receipt', receipt])
    const status = await this.exec(['data-api', 'connect', slug, '--status', '--receipt', receipt])
    requireValue(status.stage === 'verified' && status.resource_status === 'matches_receipt' && status.types_status === 'matches_receipt', 'connect_resume_status_invalid')
    const original = await readFile(receipt)
    await progress('runtime_redeployment')
    const deployment = await this.exec(['data-api', 'deploy', slug, '--timeout', '15m'])
    requireValue(deployment.id && deployment.id !== saved.deployment_id && deployment.app_id === resource.id, 'requalification_new_deployment_missing')
    await this.deployed(deployment, connectedApp)
    const refreshedApp = await this.request(`/v1/apps/${slug}`)
    requireValue(refreshedApp.id === resource.id, 'connect_app_identity_changed')
    await this.checkControlPlaneRuntime(refreshedApp)
    await this.checkServingRuntime(this.appURL({ url: refreshedApp.canonical_url || refreshedApp.url }))
    await progress('stale_receipt')
    const oldStatusOutput = await run(['--json', 'data-api', 'connect', slug, '--status', '--receipt', receipt], { expectedExitCode: 1 })
    let oldStatus
    try { oldStatus = JSON.parse(oldStatusOutput) } catch { throw new CanaryFailure('old_receipt_status_invalid') }
    requireValue(oldStatus.stage === 'verified' && oldStatus.status === 'blocked' && oldStatus.resource_status === 'changed_or_unavailable' && oldStatus.reason === 'app configuration or deployment changed', 'old_receipt_not_stale')
    const plan = join(this.workspace, 'requalify-plan.json')
    const candidateTypes = join(this.workspace, 'requalify.types.ts')
    const candidateReceipt = join(this.workspace, 'requalify-receipt.json')
    await progress('requalification')
    await run(['data-api', 'connect', slug, '--requalify-plan', plan, '--receipt', receipt, '--new-receipt', candidateReceipt, '--output', candidateTypes, '--collection', collection, '--identities', identities])
    const reviewedPlan = await readFile(plan)
    let planData
    try { planData = JSON.parse(reviewedPlan) } catch { throw new CanaryFailure('requalification_plan_invalid') }
    requireValue(planData.previous_deployment_id === saved.deployment_id && planData.candidate?.deployment_id === deployment.id && planData.candidate?.stage === 'deployed' && /^[a-f0-9]{64}$/.test(planData.candidate?.fingerprint || ''), 'requalification_plan_mismatch')
    const digest = createHash('sha256').update(reviewedPlan).digest('hex')
    await run(['data-api', 'connect', slug, '--requalify-apply', plan, '--plan-sha256', digest, '--receipt', receipt, '--collection', collection, '--identities', identities])
    const candidate = JSON.parse(await readFile(candidateReceipt, 'utf8'))
    requireValue(candidate.stage === 'verified' && candidate.app_id === resource.id && candidate.database_id === db.id && candidate.binding_id === saved.binding_id && candidate.deployment_id === deployment.id && candidate.fingerprint === planData.candidate.fingerprint && candidate.output === candidateTypes, 'requalification_receipt_invalid')
    requireValue(original.equals(await readFile(receipt)), 'original_receipt_changed')
    const candidateStatus = await this.exec(['data-api', 'connect', slug, '--status', '--receipt', candidateReceipt])
    requireValue(candidateStatus.stage === 'verified' && candidateStatus.resource_status === 'matches_receipt' && candidateStatus.types_status === 'matches_receipt', 'requalification_status_invalid')

    // A reviewed but deliberately incorrect positive assertion must fail
    // replay without producing a verified receipt. This sends GETs only.
    const wrongCollection = join(this.workspace, 'requalify-wrong.requests.json')
    const wrongPlan = join(this.workspace, 'requalify-wrong.plan.json')
    const wrongTypes = join(this.workspace, 'requalify-wrong.types.ts')
    const wrongReceipt = join(this.workspace, 'requalify-wrong.receipt.json')
    await progress('failed_authorization')
    const wrong = JSON.parse(await readFile(collection, 'utf8'))
    const positive = wrong.requests?.find(request => Array.isArray(request.expect?.json) && request.expect.json.length === 1)
    requireValue(Boolean(positive), 'requalification_positive_fixture_missing')
    positive.expect.json = [{ id: -999999 }]
    await writeFile(wrongCollection, JSON.stringify(wrong), { mode: 0o600 })
    await run(['data-api', 'connect', slug, '--requalify-plan', wrongPlan, '--receipt', receipt, '--new-receipt', wrongReceipt, '--output', wrongTypes, '--collection', wrongCollection, '--identities', identities])
    const wrongDigest = createHash('sha256').update(await readFile(wrongPlan)).digest('hex')
    await run(['data-api', 'connect', slug, '--requalify-apply', wrongPlan, '--plan-sha256', wrongDigest, '--receipt', receipt, '--collection', wrongCollection, '--identities', identities], { expectedExitCode: 1, expectedStderrIncludes: 'authorization checks failed' })
    try { await lstat(wrongReceipt); throw new CanaryFailure('failed_requalification_created_receipt') } catch (error) { if (error.code !== 'ENOENT') throw error }
    requireValue(original.equals(await readFile(receipt)), 'original_receipt_changed')

    await progress('hosted_client')
    const bundle = await this.verifiedHostedBundle()
    const hostedClient = join(this.workspace, 'hosted-client')
    await mkdir(hostedClient)
    const typescript = join(this.workspace, 'hosted-typescript')
    await cp(join(this.repoRoot, 'sdk/data/node_modules/typescript'), typescript, { recursive: true, force: false, errorOnExist: true })
    await writeFile(join(hostedClient, 'package.json'), JSON.stringify({ name: 'hosted-canary-client', version: '1.0.0', private: true, type: 'module', devDependencies: { typescript: `file:${typescript}` } }))
    await writeFile(join(hostedClient, 'tsconfig.json'), JSON.stringify({ compilerOptions: { target: 'ES2022', module: 'NodeNext', moduleResolution: 'NodeNext', strict: true, skipLibCheck: false, noEmit: true } }))
    const sentinel = join(hostedClient, 'existing.ts')
    await writeFile(sentinel, 'export const preserved = true\n')
    const npmEnv = { ...localToolEnv(), npm_config_offline: 'true' }
    await this.runCommand('npm', ['install', '--offline', '--ignore-scripts', '--no-audit', '--no-fund'], { cwd: hostedClient, env: npmEnv, signal: this.signal, timeout: Math.min(300_000, Math.max(1, this.deadline - Date.now())) })
    const assertion = join(this.workspace, 'hosted-expected.json')
    await writeFile(assertion, JSON.stringify([{ id: 1, subject: 'alice' }]), { mode: 0o600 })
    const hostedOutput = await run(['--json', 'data-api', 'integrate', '--hosted', slug, '--receipt', candidateReceipt, '--directory', hostedClient, '--bundle', bundle, '--relation', 'connect_canary_notes', '--order-by', 'id', '--assertion', assertion, '--token-env', 'GREGALE_CONNECT_ALICE'], {}, true)
    let events
    try { events = hostedOutput.trim().split('\n').map(line => JSON.parse(line)) } catch { throw new CanaryFailure('hosted_integration_result_invalid') }
    const results = events.filter(event => event.event === 'hosted_integration_result')
    requireValue(results.length === 1 && results[0].status === 'passed' && results[0].app === slug && results[0].fingerprint === candidate.fingerprint, 'hosted_integration_result_invalid')
    requireValue(await readFile(sentinel, 'utf8') === 'export const preserved = true\n', 'hosted_integration_modified_existing_source')
    this.report.connected_journey = { original_deployment_id: saved.deployment_id, candidate_deployment_id: deployment.id, schema_fingerprint: candidate.fingerprint, stale_receipt_rejected: true, failed_assertion_rejected: true, hosted_client_verified: true }
    await this.save()
  }
  async run() {
    this.workspace = await mkdtemp(join(tmpdir(), 'gregale-data-api-canary-'))
    try {
      await this.save()
      if (this.config.connect) await this.freezeHostedBundle()
      await this.check('staging_account', async () => requireValue((await this.request('/v1/account')).id === this.config.account_id, 'staging_account_mismatch'))
      const app = await this.check('candidate_control_plane_runtime', async () => {
        const app = await this.createApp('api')
        await this.checkControlPlaneRuntime(app)
        return app
      })
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
      const jwt = (sub, audience = this.prefix, lifetime = 300, tenant = 'canary') => {
        const enc = value => Buffer.from(JSON.stringify(value)).toString('base64url')
        const now = Math.floor(Date.now() / 1000)
        const data = `${enc({ alg: 'ES256', kid: this.prefix })}.${enc({ sub, iss: issuerURL, aud: audience, exp: now + lifetime, iat: now, tenant, role: 'postgres' })}`
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
      for (const file of ['migrate.mjs', 'migration-server.mjs', 'load-fixture.sql']) await cp(join(this.repoRoot, 'tests/data-api/staging', file), join(migrationDir, file))
      await mkdir(join(migrationDir, 'rpc-runtime'))
      for (const file of ['types.mjs', 'type-render.mjs', 'config.mjs']) await cp(join(this.repoRoot, 'cmd/gregale/templates/data-api', file), join(migrationDir, 'rpc-runtime', file))
      for (const [source, target] of [['release.mjs', 'release.mjs'], ['rpc-permissions.mjs', 'rpc-permissions.mjs']])
        await cp(join(this.repoRoot, 'cmd/gregale/templates/data-api-starter/migrations', source), join(migrationDir, target))
      await writeFile(join(migrationDir, 'version'), '1')
      if (this.config.connect) await writeFile(join(migrationDir, 'connect-fixture'), '')
      if (this.config.mode === 'smoke') await writeFile(join(migrationDir, 'large-contract'), '')
      await writeFile(join(migrationDir, 'gregale.yaml'), 'release:\n  command: node migrate.mjs\n')
      await writeFile(join(migrationDir, 'Dockerfile'), 'FROM node:22-bookworm-slim\nWORKDIR /app\nCOPY package*.json ./\nRUN npm ci --omit=dev --ignore-scripts\nCOPY . .\nEXPOSE 8080\nCMD ["node","migration-server.mjs"]\n')
      await this.check('migration_release_native_build', () => this.deploy(migrator, migrationDir))
      await this.check('migration_credential_release_only', async () => {
        const response = await this.publicFetch(this.appURL(migrator) + '/healthz')
        requireValue(response.ok && (await response.json()).migration_credential_present === false, 'migration_runtime_custody_failed')
      })
      await this.check('data_api_native_build', async () => this.deployed(await this.exec(['data-api', 'create', app.slug, '--database', db.id, '--issuer', issuerURL, '--jwks-url', issuerURL + '/jwks.json', '--audience', this.prefix, '--resume']), app))
      if (this.config.write_inspection) await this.check('write_inspection_binding_identity', () => this.qualifyWriteInspection({ db, app }))
      const url = this.appURL(app)
      await this.check('candidate_serving_runtime', () => this.checkServingRuntime(url))
      if (this.config.connect) await this.check('read_only_connect_and_resume', () => this.qualifyConnect({ db, app, issuerURL, jwt }))
      const clientDir = join(this.workspace, 'client'); await mkdir(clientDir)
      await writeFile(join(clientDir, 'package.json'), '{"type":"module"}')
      await this.runCommand('npm', ['install', '--ignore-scripts', '--no-audit', '--no-fund', this.sdkTarball], { cwd: clientDir, env: localToolEnv(), signal: this.signal })
      const smoke = this.config.mode === 'smoke'
      const fixtureClient = smoke ? 'smoke-client' : 'workflow-client'
      for (const file of ['client.ts', fixtureClient + '.ts']) await cp(join(this.repoRoot, 'tests/data-api/staging', file), join(clientDir, file))
      await this.check('rpc_denied_before_permission_setup', async () => {
        const response = await this.publicFetch(url + '/rest/v1/rpc/create_note', { method: 'POST', headers: { Authorization: `Bearer ${jwt('alice')}`, 'Content-Type': 'application/json' }, body: '{"note_body":"must not execute"}' })
        requireValue(response.status === 404, 'rpc_exposed_without_grant')
      })
      await writeFile(join(migrationDir, 'gregale.yaml'), 'release:\n  command: node release.mjs\n')
      await this.check('rpc_permissions_sync_native_build', () => this.sync(app, migrator, migrationDir, clientDir, jwt('alice', this.prefix, 1800)))
      await this.check('serving_fingerprint_agreement', () => this.contract(url, jwt('alice'), this.report.syncs.at(-1).fingerprint))
      if (smoke) await this.check('large_contract_download', async () => {
        const output = join(clientDir, 'database.large.types.ts')
        await this.exec(['data-api', 'types', app.slug, '--output', output], clientDir)
        const content = await readFile(output, 'utf8')
        const bytes = Buffer.byteLength(content)
        requireValue(bytes > 1024 * 1024 && bytes <= 8 * 1024 * 1024, 'large_contract_size_invalid')
        requireValue(content.includes('"smoke_large_079"') && content.includes('"smoke_field_069' + '_'.repeat(45) + '"'), 'large_contract_fixture_missing')
        requireValue(content.includes(`// Schema fingerprint: ${this.report.syncs.at(-1).fingerprint}\n`), 'large_contract_fingerprint_mismatch')
        await this.runCommand(join(this.repoRoot, 'sdk/data/node_modules/.bin/tsc'), ['--strict', '--target', 'ES2022', '--module', 'NodeNext', '--noEmit', 'database.large.types.ts'], { cwd: clientDir, env: localToolEnv(), signal: this.signal })
        this.report.large_contract = { bytes }
      })
      if (!smoke) await this.exec(['data-api', 'types', app.slug, '--output', join(clientDir, 'database.types.ts'), '--check'])
      const client = await this.loadModule(join(clientDir, 'dist', smoke ? 'smoke-client.js' : 'client.js'))
      const clientFetch = (input, init) => this.publicFetch(input, init)
      const id = await this.check('typed_crud_and_subject_isolation', () => client.exercise(url, jwt('alice'), jwt('bob'), clientFetch))
      if (smoke) {
        await this.check('exact_bigint_round_trip', () => client.exerciseExact(url, jwt('alice'), id, clientFetch))
        await this.check('public_cache_json_then_csv', () => this.checkPublicCache(app, jwt('alice')))
      } else {
        await this.runFullChecks({ app, db, migrator, migrationDir, clientDir, url, jwt, clientFetch, id })
      }
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
  async runFullChecks({ app, db, migrator, migrationDir, clientDir, url, jwt, clientFetch, id }) {
    const workflowClient = await this.loadModule(join(clientDir, 'dist/workflow-client.js'))
    const { createDataClient } = await this.loadModule(join(clientDir, 'node_modules/@gregale/data/dist/index.js'))
    const alice = createDataClient({ url, accessToken: () => jwt('alice'), fetch: clientFetch }).schema('api')
    await this.check('same_subject_tenant_isolation', async () => {
      const otherTenant = createDataClient({ url, accessToken: () => jwt('alice', this.prefix, 300, 'other-tenant'), fetch: clientFetch }).schema('api')
      const hidden = await otherTenant.from('notes').select('id').eq('id', id).retry(false)
      requireValue(!hidden.error && hidden.data?.length === 0, 'cross_tenant_read_allowed')
      const denied = await otherTenant.from('notes').insert({ subject: 'alice', body: 'forbidden-tenant' }).retry(false)
      requireValue(denied.error?.code === '42501', 'cross_tenant_write_allowed')
    })
    const rpcCheck = async () => {
      const observations = []
      await workflowClient.exerciseRPC(url, jwt('alice'), jwt('bob'), clientFetch, info => { observations.push(info) })
      requireValue(observations.length > 0, 'rpc_diagnostics_missing')
      await this.correlate(app, observations[0], 'POST', 'rpc')
    }
    await this.check('typed_rpc_subject_isolation_and_logs', rpcCheck)
    await this.check('sdk_request_id_runtime_log_correlation', async () => {
      const observations = []
      const probe = createDataClient({ url, accessToken: () => jwt('alice'), fetch: clientFetch, onResponse: info => { observations.push(info) } }).schema('api')
      const response = await probe.from('notes').select('id').eq('id', id).single().retry(false)
      requireValue(!response.error && response.data.id === id && observations.length === 1, 'sdk_probe_failed')
      await this.correlate(app, observations[0], 'GET', 'rest')
    })
    await this.check('jwt_audience_and_openapi', async () => {
      const bad = await this.publicFetch(url + '/rest/v1/notes', { headers: { Authorization: `Bearer ${jwt('alice', 'wrong')}` } })
      requireValue(bad.status === 401, 'wrong_audience_accepted')
      requireValue((await this.publicFetch(url + '/__gregale/schema')).status === 401, 'serving_contract_auth_missing')
      requireValue((await this.publicFetch(url + '/rest/v1/rpc/private_note', { method: 'POST', headers: { Authorization: `Bearer ${jwt('alice')}`, 'Content-Type': 'application/json' }, body: '{}' })).status === 404, 'unapproved_rpc_exposed')
      const openapi = await this.publicFetch(url + '/openapi.json', { headers: { Authorization: `Bearer ${jwt('alice')}` } })
      const document = await openapi.json()
      requireValue(openapi.ok && document.paths?.['/notes'] && document.paths?.['/rpc/create_note']?.post && !document.paths?.['/rpc/create_note']?.get && !document.paths?.['/rpc/private_note'], 'public_openapi_missing')
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
    const olderPath = await this.captureContract(app, clientDir, 'older')
    await writeFile(join(migrationDir, 'version'), '2')
    const beforeMigration = this.report.syncs.at(-1).fingerprint
    await this.check('schema_migration_sync_native_build', () => this.sync(app, migrator, migrationDir, clientDir, jwt('alice', this.prefix, 1800)))
    await this.check('migrated_type_and_rest_contract', async () => {
      const types = await readFile(join(clientDir, 'database.types.ts'), 'utf8')
      requireValue(types.includes('"priority"') && this.report.syncs.at(-1).fingerprint !== beforeMigration, 'migrated_type_missing')
      await this.contract(url, jwt('alice'), this.report.syncs.at(-1).fingerprint)
      const result = await alice.from('notes').select('priority').eq('id', id).single()
      requireValue(!result.error && result.data.priority === 0, 'migrated_rest_contract_missing')
    })
    await this.check('credential_rotation_preserves_data', async () => {
      const bindings = await this.exec(['postgres', 'bindings', 'list', db.id])
      const binding = bindings.items.filter(x => x.app_id === app.id && x.access === 'data_api')
      requireValue(binding.length === 1, 'data_api_binding_missing')
      requireValue(Number.isSafeInteger(binding[0].credential_generation) && binding[0].credential_generation > 0, 'rotation_generation_missing')
      const replacement = await this.exec(['postgres', 'bindings', 'rotate', binding[0].id, '--wait', '--wait-timeout', '5m'])
      requireValue(replacement.id === binding[0].id && replacement.app_id === app.id && replacement.access === 'data_api' && replacement.state === 'ready' && replacement.rotation_pending === false && replacement.credential_generation === binding[0].credential_generation + 1, 'rotation_not_completed')
      this.report.rotation = { binding_id: replacement.id, previous_generation: binding[0].credential_generation, current_generation: replacement.credential_generation, completed: true }; await this.save()
      await this.sync(app, migrator, migrationDir, clientDir, jwt('alice', this.prefix, 1800))
      await this.contract(url, jwt('alice'), this.report.syncs.at(-1).fingerprint)
      await rpcCheck()
      const result = await alice.from('notes').select('id,body,priority').eq('id', id).single()
      requireValue(!result.error && result.data.body === 'updated' && result.data.priority === 0, 'rotated_credential_data_lost')
      const bob = createDataClient({ url, accessToken: () => jwt('bob'), fetch: clientFetch }).schema('api')
      const hidden = await bob.from('notes').select('id').eq('id', id)
      requireValue(!hidden.error && hidden.data.length === 0, 'rotated_credential_rls_lost')
      const deleted = await alice.from('notes').delete().eq('id', id).select('id')
      requireValue(!deleted.error && deleted.data.length === 1, 'typed_delete_failed')
    })
    const currentPath = await this.captureContract(app, clientDir, 'current')
    await this.qualifyRollout({ app, url, token: () => jwt('alice'), createDataClient, olderPath, currentPath, clientDir })
    await this.qualifyLoad({ app, url, jwt, clientDir })
  }
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  try {
    const { values: { 'check-config': checkConfigOnly, mode } } = parseArgs({
      options: { 'check-config': { type: 'boolean' }, mode: { type: 'string' } },
    })
    const config = validateConfig(JSON.parse(await readFile(process.env.FAAS_DATA_API_STAGING_CONFIG, 'utf8')), process.env, { checkConfigOnly, mode })
    if (checkConfigOnly) console.log('Data API staging configuration passed; no provider calls made')
    else {
      const interrupt = new AbortController()
      process.once('SIGINT', () => interrupt.abort())
      process.once('SIGTERM', () => interrupt.abort())
      const runner = new Canary(config, { evidence: resolve(process.env.FAAS_DATA_API_EVIDENCE || 'data-api-staging-evidence.json'), cli: process.env.GREGALE_CANARY_BIN, sdkTarball: process.env.FAAS_DATA_API_SDK_TARBALL, sdkBundle: process.env.FAAS_DATA_API_SDK_BUNDLE, repoRoot: resolve(new URL('../../../', import.meta.url).pathname), signal: interrupt.signal })
      const report = await runner.run()
      console.log(`Data API staging ${report.mode} canary passed ${report.checks.length} checks and cleanup`)
    }
  } catch (error) {
    const code = error instanceof CanaryFailure ? error.code : 'configuration_or_canary_failure'
    console.error(`Data API staging canary failed (${code}); inspect configuration and evidence when present`)
    process.exitCode = 1
  }
}
