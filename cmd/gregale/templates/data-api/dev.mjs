// Local-only entrypoint. Production continues to use server.mjs unchanged.
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { randomBytes, createHash } from 'node:crypto'
import http from 'node:http'
import { readFile, writeFile, rename, unlink, mkdir, lstat, readdir, open } from 'node:fs/promises'
import { constants } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
import { setTimeout as delay } from 'node:timers/promises'
import pg from 'pg'
import { generateKeyPair, exportJWK, createLocalJWKSet, SignJWT } from 'jose'
import { runtimeConfig, acceptedContracts, servingContracts, limits } from './config.mjs'
import { createServer, tokenVerifier } from './server.mjs'
import { inspect, generate, fingerprint } from './types.mjs'
import { migrate } from './migrate.mjs'
import { requestStore } from './dev-requests.mjs'
import { replayRequest, collectionScenarios } from './request-replay.mjs'
import { inspector } from './dev-inspector.mjs'
import { permissions } from './rpc-permissions.mjs'
import { loadDevProject, verifyProject } from './dev-project.mjs'

const execute = promisify(execFile)
const postgrestImage = 'postgrest/postgrest@sha256:7c5840727ad683b0fdbfb9810930665b34f8b44884a4dc740aa8844748a2b85e'
class DevFailure extends Error {}
class BreakingContract extends DevFailure {
  constructor() { super('Breaking schema changes rejected; add a compatible migration, or revise the baseline and restart development. The saved contract and generated types were preserved.') }
}

async function readContract(path, required) {
  try {
    if (!(await lstat(path)).isFile()) throw new DevFailure('Compatibility baseline must be a regular file')
    const file = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK)
    try {
      if (!(await file.stat()).isFile()) throw new DevFailure('Contract must be a regular file')
      const buffer = Buffer.alloc(limits.outputBytes + 1)
      let length = 0
      while (length < buffer.length) {
        const { bytesRead } = await file.read(buffer, length, buffer.length - length, length)
        if (!bytesRead) break
        length += bytesRead
      }
      if (length > limits.outputBytes) throw new DevFailure('Compatibility baseline exceeds the contract output limit')
      return buffer.subarray(0, length).toString('utf8')
    } finally { await file.close() }
  } catch (error) {
    if (error.code === 'ENOENT' && !required) return null
    throw error instanceof DevFailure ? error : new DevFailure('Cannot read compatibility baseline; initialize one with data-api dev --once or use --baseline FILE.')
  }
}

async function compareContract(before, snapshot, cli, signal, approval = '') {
  const suffix = randomBytes(12).toString('hex')
  const baseline = join(import.meta.dirname, `diff-before-${suffix}.json`)
  const current = join(import.meta.dirname, `diff-after-${suffix}.json`)
  try {
    await atomicFile(baseline, before, 0o600)
    await atomicFile(current, JSON.stringify({ version: 1, fingerprint: fingerprint(snapshot), snapshot }), 0o600)
    // Use the existing Go comparator, including its strict contract validation.
    const args = ['--json', 'data-api', 'diff', '--baseline', baseline, '--current', current]
    if (approval) args.push('--approval', approval)
    const result = await execute(cli, args, { signal, timeout: 30000, maxBuffer: 2 << 20 })
    const report = JSON.parse(result.stdout)
    if (!Number.isInteger(report.breaking) || report.breaking < 0 || !Array.isArray(report.changes) || report.current_fingerprint !== fingerprint(snapshot)) throw new Error('Invalid comparison')
    const approved = report.approved ?? 0
    const unapproved = report.unapproved ?? report.breaking
    if (!Number.isInteger(approved) || approved < 0 || !Number.isInteger(unapproved) || unapproved < 0 || approved + unapproved !== report.breaking) throw new Error('Invalid approval counts')
    return { baseline_fingerprint: report.baseline_fingerprint, current_fingerprint: report.current_fingerprint, breaking: report.breaking, approved, unapproved, changes: report.changes }
  } catch { throw new DevFailure('Schema compatibility comparison failed; check the saved contracts and any approval fingerprints and named changes.') }
  finally { await unlink(baseline).catch(() => {}); await unlink(current).catch(() => {}) }
}

function compatibilityReport(report, json) {
  if (json) console.log(JSON.stringify({ status: 'compatibility', ...report }))
  else {
    for (const change of report.changes) console.log(`${change.approved ? 'approved breaking' : change.breaking ? 'breaking' : 'compatible'} ${JSON.stringify(change.path)}: ${change.reason}`)
    console.log(`${report.changes.length} schema changes, ${report.breaking} breaking, ${report.approved} approved, ${report.unapproved} unapproved.`)
  }
}

// Content polling handles atomic editor saves and directory replacement across
// Linux/macOS and Docker-mounted workspaces without overlapping reloads.
export async function migrationSource(directory) {
  const hash = createHash('sha256')
  for (const entry of (await readdir(directory, { withFileTypes: true })).sort((a, b) => a.name.localeCompare(b.name))) {
    hash.update(JSON.stringify([entry.name, entry.isDirectory() ? 'directory' : createHash('sha256').update(await readFile(join(directory, entry.name))).digest('hex')]))
  }
  return hash.digest('hex')
}

export async function watchMigrations({ directory, initial, signal, reload, report, source = () => migrationSource(directory) }) {
  let attempted = initial, candidate = initial, readFailed = false
  while (!signal.aborted) {
    try { await delay(250, undefined, { signal }) } catch { break }
    let current
    try { current = await source(); readFailed = false }
    catch {
      if (!readFailed) await report('source_unavailable')
      readFailed = true
      // Returning to the same contents after a directory/read failure must retry.
      attempted = candidate = null
      continue
    }
    if (current === attempted) continue
    if (current !== candidate) { candidate = current; continue }
    attempted = current
    try { await reload() }
    catch (error) { if (!signal.aborted) await report('reload_failed', error) }
  }
}

function migrationDiagnostic(error) {
  if (error instanceof DevFailure) return error.message
  if (error?.message === 'An applied migration was changed or removed; add a new migration instead') return 'Restore the changed or removed applied migration and add a new migration instead.'
  if (error?.message === 'New migrations must follow the applied versions') return 'Give new migrations a version higher than the latest applied version.'
  if (error?.message === 'Use numbered SQL files such as 0001_notes.sql' || error?.message === 'Migration versions must be positive and unique') return 'Use unique, positive, numbered SQL files such as 0011_new_column.sql.'
  // Database error text can contain credentials or application data.
  return /^[0-9A-Z]{5}$/.test(error?.code ?? '') ? `SQLSTATE ${error.code}; fix the pending SQL migration.` : 'Check the migration SQL, schema permissions and client dependencies.'
}

export function localDockerHost(value) {
  if (/^(unix|npipe):\/\//.test(value)) return true
  try { const url = new URL(value); return url.protocol === 'tcp:' && ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname) }
  catch { return false }
}

async function atomicFile(path, content, mode) {
  await mkdir(dirname(path), { recursive: true })
  try { if (!(await lstat(path)).isFile()) throw new Error('Output must be a regular file') }
  catch (error) { if (error.code !== 'ENOENT') throw error }
  const temporary = `${path}.${randomBytes(8).toString('hex')}.tmp`
  try { await writeFile(temporary, content, { mode, flag: 'wx' }); await rename(temporary, path) }
  finally { await unlink(temporary).catch(() => {}) }
}

export async function run(options) {
  if (Number(process.versions.node.split('.')[0]) < 22) throw new Error('Node.js 22 or newer is required')
  if (options.check_types && !options.once) throw new DevFailure('--check-types requires --once.')
  if (options.replay && !options.once) throw new DevFailure('--replay requires --once.')
  if (options.scenario && !options.replay) throw new DevFailure('--scenario requires --replay.')
  let project
  try { project = await loadDevProject(options) }
  catch (error) { throw new DevFailure(`Invalid development project: ${error.message}`) }
  options = { ...options, output: project.output }
  const readPolicy = async () => {
    if (!options.accepted_contracts) return undefined
    try { return acceptedContracts(await readContract(options.accepted_contracts, true)) }
    catch { throw new DevFailure('Cannot read accepted contracts; use a valid, bounded regular policy file.') }
  }
  if (options.accepted_contracts) {
    const policyInfo = await lstat(options.accepted_contracts)
    for (const path of [options.output, options.baseline || join(options.directory, '.gregale', 'data-api-contract.json')]) {
      const info = await lstat(path).catch(() => null)
      if (resolve(path) === resolve(options.accepted_contracts) || (info && info.dev === policyInfo.dev && info.ino === policyInfo.ino)) throw new DevFailure('Accepted contracts must not alias generated types or the baseline.')
    }
    await readPolicy()
  }
  let replayCollection, scenarios
  if (options.replay) {
    try { replayCollection = await requestStore(options.replay, { identities: Object.keys(project.identities) }).read(true) }
    catch { throw new DevFailure('Cannot read replay collection; check the path, version 1 or 2 format, size and file type.') }
    try { scenarios = collectionScenarios(replayCollection, options.scenario) }
    catch { throw new DevFailure('Unknown replay scenario; select a name present in the collection.') }
  }
  if (!options.cli) throw new DevFailure('Start local development through gregale data-api dev so it can use the schema comparator.')
  const controller = new AbortController()
  let stopped = false, server, receiptWritten = false, phase = 'Docker setup'
  const stop = () => { stopped = true; controller.abort() }
  process.on('SIGINT', stop); process.on('SIGTERM', stop)
  const startup = setTimeout(() => controller.abort(), 300000)
  const suffix = randomBytes(12).toString('hex')
  const database = `gregale-dev-db-${suffix}`, rest = `gregale-dev-rest-${suffix}`
  // Separate sessions can coexist in a project without sharing receipts.
  const receipt = join(options.directory, '.gregale', `data-api-dev-${suffix}.json`)
  const docker = async (args, cleanup = false) => {
    try {
      return (await execute('docker', args, { signal: cleanup || args[0] === 'run' ? undefined : controller.signal, timeout: cleanup || args[0] === 'run' ? 30000 : 300000, maxBuffer: 1 << 20 })).stdout.trim()
    } catch { throw new Error(`Docker ${args[0]} failed; check your local daemon, image availability and ports`) }
  }
  let cleanupFailed = false
  let servicesAttempted = false
  try {
    const contractPath = options.baseline || join(options.directory, '.gregale', 'data-api-contract.json')
    let acceptedContract = await readContract(contractPath, Boolean(options.baseline || options.check_breaking))
    let compatibility = null
    const checkCompatibility = async snapshot => {
      if (!acceptedContract) return
      compatibility = await compareContract(acceptedContract, snapshot, options.cli, controller.signal, options.approval)
      compatibilityReport(compatibility, options.json)
      if (options.check_breaking && compatibility.unapproved) throw new BreakingContract()
    }
    const acceptContract = async snapshot => {
      const content = JSON.stringify({ version: 1, fingerprint: fingerprint(snapshot), snapshot }) + '\n'
      if (!options.baseline) { await atomicFile(contractPath, content, 0o600); acceptedContract = content }
      // An explicitly selected baseline is read-only and stays fixed for CI.
    }
    const context = JSON.parse(await docker(['context', 'inspect']))
    const host = process.env.DOCKER_CONTEXT ? context[0]?.Endpoints?.docker?.Host : (process.env.DOCKER_HOST || context[0]?.Endpoints?.docker?.Host)
    if (!localDockerHost(host)) throw new Error('Select a local Docker context')
    await docker(['info', '--format', '{{.ServerVersion}}'])
    phase = 'pulling local service images'
    for (const image of ['postgres:16', postgrestImage]) {
      try { await docker(['image', 'inspect', image, '--format', '{{.Id}}']) }
      catch { controller.signal.throwIfAborted(); await docker(['pull', image]) }
    }
    phase = 'PostgreSQL startup'
    const password = randomBytes(32).toString('hex')
    // Passwords live in private environment files, never process arguments or logs.
    const envFile = join(import.meta.dirname, `postgres-${suffix}.env`)
    await writeFile(envFile, `POSTGRES_PASSWORD=${password}\nPOSTGRES_DB=gregale_dev\n`, { mode: 0o600, flag: 'wx' })
    servicesAttempted = true
    await docker(['run', '--detach', '--name', database, '--label', 'gregale.data-api.dev=true', '--env-file', envFile,
      '--publish', '127.0.0.1::5432', '--publish', '127.0.0.1::3000', '--publish', '127.0.0.1::3001', 'postgres:16'])
    const port = async number => {
      const address = await docker(['port', database, `${number}/tcp`])
      if (!/^127\.0\.0\.1:\d+$/.test(address)) throw new Error('Docker did not bind to loopback')
      return Number(address.split(':')[1])
    }
    const databasePort = await port(5432), upstreamPort = await port(3000), readyPort = await port(3001)
    const ownerURL = `postgresql://postgres:${password}@127.0.0.1:${databasePort}/gregale_dev?sslmode=disable`
    const owner = new pg.Client({ connectionString: ownerURL, connectionTimeoutMillis: 1000 })
    // A failed pg.Client connection cannot be reused.
    for (;;) {
      controller.signal.throwIfAborted()
      const probe = new pg.Client({ connectionString: ownerURL, connectionTimeoutMillis: 1000 })
      try { await probe.connect(); break } catch { await delay(100, undefined, { signal: controller.signal }) }
      finally { await probe.end().catch(() => {}) }
    }
    await owner.connect()
    try {
      await owner.query(`REVOKE ALL ON DATABASE gregale_dev FROM PUBLIC;
        REVOKE ALL ON SCHEMA public FROM PUBLIC;
        CREATE ROLE gregale_data_api LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD '${password}';
        ALTER ROLE gregale_data_api SET statement_timeout='15000';
        GRANT CONNECT ON DATABASE gregale_dev TO gregale_data_api;`)
    } finally { await owner.end() }
    phase = 'SQL migrations'
    const migrationDirectory = project.migrations
    const watchedSource = async () => (await migrationSource(migrationDirectory)) + (options.accepted_contracts ? createHash('sha256').update(await readContract(options.accepted_contracts, true)).digest('hex') : '')
    const initialSource = await watchedSource()
    const applyMigrations = () => migrate(ownerURL, pathToFileURL(migrationDirectory + '/'))
    await applyMigrations()
    if (project.seedSQL !== null) {
      phase = 'local seed data'
      const seed = new pg.Client({ connectionString: ownerURL })
      try { await seed.connect(); await seed.query('BEGIN'); await seed.query("SET LOCAL statement_timeout='15s'"); await seed.query(project.seedSQL); await seed.query('COMMIT') }
      catch (error) { await seed.query('ROLLBACK').catch(() => {}); throw error }
      finally { await seed.end() }
    }
    const grants = new pg.Client({ connectionString: ownerURL })
    await grants.connect()
    try {
      await grants.query(`GRANT USAGE ON SCHEMA api TO gregale_data_api;
        GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA api TO gregale_data_api;
        GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA api TO gregale_data_api;
        ALTER DEFAULT PRIVILEGES IN SCHEMA api GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO gregale_data_api;
        ALTER DEFAULT PRIVILEGES IN SCHEMA api GRANT USAGE,SELECT ON SEQUENCES TO gregale_data_api;`)
    } finally { await grants.end() }
    phase = 'RLS and RPC permission checks'
    await permissions(ownerURL, { role: 'gregale_data_api', apply: true })
    phase = 'schema introspection'
    const connection = new URL(ownerURL); connection.username = 'gregale_data_api'
    const config = runtimeConfig({ DATABASE_URL: connection.toString(), DATA_API_ISSUER: 'https://gregale-dev.invalid',
      DATA_API_JWKS_URL: 'https://gregale-dev.invalid/jwks', DATA_API_AUDIENCE: 'gregale-local-dev',
      DATA_API_ALLOWED_ORIGINS: 'http://localhost:5173,http://127.0.0.1:5173' })
    config.snapshot = await inspect(connection.toString(), ['api'])
    config.functions = config.snapshot.functions
    config.fingerprint = fingerprint(config.snapshot)
    config.acceptedContracts = await readPolicy()
    try { servingContracts(config) } catch { throw new DevFailure('Accepted contracts are stale; review a policy for the current schema before starting.') }
    phase = 'schema compatibility'
    await checkCompatibility(config.snapshot)
    const internal = new URL(connection); internal.port = '5432'
    const postgrestEnv = { ...config.postgrestEnv, PGRST_DB_URI: internal.toString(), PGRST_SERVER_HOST: '0.0.0.0' }
    const restEnvFile = join(import.meta.dirname, `postgrest-${suffix}.env`)
    await writeFile(restEnvFile, Object.entries(postgrestEnv).map(([key, value]) => `${key}=${value}`).join('\n') + '\n', { mode: 0o600, flag: 'wx' })
    // Share only this disposable database's network namespace. All exposed ports are loopback-bound.
    // Wait for each run request to finish even on interruption: the daemon may
    // still create its container after the client is disconnected.
    phase = 'PostgREST and gateway startup'
    await docker(['run', '--detach', '--name', rest, '--label', 'gregale.data-api.dev=true', '--network', `container:${database}`, '--env-file', restEnvFile, postgrestImage])
    const { privateKey, publicKey } = await generateKeyPair('ES256')
    const jwk = await exportJWK(publicKey)
    const verify = tokenVerifier(config.auth, createLocalJWKSet({ keys: [jwk] }))
    let gateway = createServer(config, verify, upstreamPort, readyPort, () => {}).listeners('request')[0]
    let inspectorState = { status: 'starting', ready: false }
    const browser = await inspector(() => inspectorState, join(options.directory, 'data-api.requests.json'), Object.keys(project.identities))
    server = http.createServer((req, res) => {
      if (browser.handle(req, res)) return
      if (gateway) return gateway(req, res)
      res.writeHead(503, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' })
      res.end(JSON.stringify({ ready: false, code: 'local_schema_refresh_pending' }))
    })
    await new Promise((resolve, reject) => { server.once('error', reject); server.listen(options.port, '127.0.0.1', resolve) })
    const url = `http://127.0.0.1:${server.address().port}`
    for (;;) {
      controller.signal.throwIfAborted()
      if ((await fetch(`${url}/healthz`, { signal: controller.signal })).ok) break
      await delay(100, undefined, { signal: controller.signal })
    }
    const identities = {}
    for (const [name, identity] of Object.entries(project.identities)) identities[name] = { subject: identity.subject, token: await new SignJWT({ ...identity.claims, sub: identity.subject })
      .setProtectedHeader({ alg: 'ES256' }).setIssuer(config.auth.issuer).setAudience(config.auth.audience).setExpirationTime('24h').sign(privateKey) }
    phase = 'required project authorization verification'
    await verifyProject(url, identities, project, AbortSignal.any([controller.signal, AbortSignal.timeout(30000)]))
    phase = options.check_types ? 'checking committed types' : 'writing generated types'
    const generatedTypes = generate(config.snapshot)
    if (options.check_types) {
      let committedTypes
      try { committedTypes = await readFile(options.output) }
      catch { throw new DevFailure('Committed types are missing or unreadable; regenerate with gregale data-api dev --once, then commit the types file.') }
      if (!committedTypes.equals(Buffer.from(generatedTypes))) throw new DevFailure('Database types are stale; regenerate with gregale data-api dev --once using the same project, config and output options, then commit the types file.')
    } else await atomicFile(options.output, generatedTypes, 0o644)
    await acceptContract(config.snapshot)
    const checkClient = async () => {
      if (!options.check) return
      for (const command of project.check.commands) {
        const task = command[0] === 'npm' ? (command[1] === 'run' ? command[2] : command[1]) : command[0]
        phase = `client ${task} (install the pinned SDK and client dependencies first)`
        try { await execute(command[0], command.slice(1), { cwd: project.check.directory, signal: controller.signal, timeout: 120000, maxBuffer: 1 << 20 }) }
        catch { throw new Error(`Client ${task} failed; install the pinned SDK and client dependencies first, then check your client code`) }
      }
    }
    await checkClient()
    clearTimeout(startup)
    if (replayCollection) {
      phase = 'saved request replay'
      let failed = 0, total = 0
      for (const scenario of scenarios) {
        const variables = new Map()
        let scenarioFailed = 0
        for (const request of scenario.requests) {
          const result = await replayRequest(request, { url, identities, signal: controller.signal, variables })
          total++
          if (!result.passed) { failed++; scenarioFailed++ }
          if (options.json) console.log(JSON.stringify({ event: 'replay_result', ...(replayCollection.version === 2 ? { scenario: scenario.name } : {}), ...result }))
          else console.log(`${result.passed ? 'PASS' : 'FAIL'} ${JSON.stringify(scenario.name)}/${JSON.stringify(result.name)}: ${result.error ?? `HTTP ${result.status}, expected ${result.expected_status}; JSON ${result.json_matches ? 'matched' : 'mismatched'}`}`)
        }
        const report = { event: 'replay_scenario_summary', scenario: scenario.name, total: scenario.requests.length, passed: scenario.requests.length - scenarioFailed, failed: scenarioFailed }
        if (replayCollection.version === 2) {
          if (options.json) console.log(JSON.stringify(report))
          else console.log(`Scenario ${JSON.stringify(scenario.name)}: ${report.passed}/${report.total} checks passed.`)
        }
      }
      const summary = { event: 'replay_summary', total, passed: total - failed, failed }
      if (options.json) console.log(JSON.stringify(summary))
      else console.log(`Replay: ${summary.passed}/${summary.total} checks passed.`)
      if (failed) throw new DevFailure('Saved request replay failed; see the assertion results above.')
    }
    phase = 'writing the private identity file'
    const inspectorURL = browser.url(url)
    const writeReceipt = async (status = 'ready', clientChecked = options.check) => {
      inspectorState = { url, fingerprint: config.fingerprint, snapshot: config.snapshot, identities, status, client_checked: clientChecked, ready: Boolean(gateway), compatibility }
      await atomicFile(receipt, JSON.stringify({ ...inspectorState, snapshot: undefined, output: options.output, inspector_url: inspectorURL }, null, 2) + '\n', 0o600)
    }
    await writeReceipt()
    receiptWritten = true
    clearTimeout(startup)
    if (options.json) console.log(JSON.stringify({ status: options.once ? 'verified' : 'ready', inspector_url: options.once ? null : inspectorURL, url, fingerprint: config.fingerprint, output: options.output, identities_file: options.once ? null : receipt, rls_verified: true, verification_scenarios: project.verification.scenarios, client_checked: options.check, types_checked: Boolean(options.check_types) }))
    else console.log(`Local Data API ready: ${url}\n${options.once ? '' : `Inspector: ${inspectorURL}\n`}Types: ${options.output}\n${options.once ? 'Local verification complete; removing disposable services.' : `Test identities (private file): ${receipt}\nCtrl-C removes the disposable database and identities.`}\n${project.legacy ? 'Alice/Bob RLS isolation verified.' : 'Required project authorization scenarios passed.'}`)
    const event = (status, detail) => {
      inspectorState = { ...inspectorState, status, detail, ready: Boolean(gateway) }
      if (options.json) console.log(JSON.stringify({ status, url, fingerprint: config.fingerprint, output: options.output, ready: Boolean(gateway), detail }))
      else (status === 'reload_failed' || status === 'source_unavailable' ? console.error : console.log)(`${status === 'reloaded' ? 'Local Data API reloaded' : status === 'reloading' ? 'Reloading local Data API' : 'Local Data API reload failed'}: ${detail}`)
    }
    const pause = () => { gateway = null; server.closeAllConnections() }
    const reload = async () => {
      pause()
      compatibility = null
      await writeReceipt('reloading', false)
      event('reloading', 'Applying migration changes; the gateway is temporarily not ready.')
      phase = 'SQL migrations'
      await applyMigrations()
      controller.signal.throwIfAborted()
      phase = 'RLS and RPC permission checks'
      await permissions(ownerURL, { role: 'gregale_data_api', apply: true })
      controller.signal.throwIfAborted()
      phase = 'schema introspection'
      const snapshot = await inspect(connection.toString(), ['api'])
      controller.signal.throwIfAborted()
      const nextConfig = { ...config, snapshot, functions: snapshot.functions, fingerprint: fingerprint(snapshot), acceptedContracts: await readPolicy() }
      phase = 'accepted contracts'
      try { servingContracts(nextConfig) } catch { throw new DevFailure('Accepted contracts are stale; save a reviewed policy for the migrated schema. Types and the saved contract were preserved.') }
      phase = 'schema compatibility'
      await checkCompatibility(snapshot)
      phase = 'PostgREST schema refresh'
      await docker(['restart', '--time', '5', rest])
      const readySignal = AbortSignal.any([controller.signal, AbortSignal.timeout(30000)])
      for (;;) {
        readySignal.throwIfAborted()
        try { if ((await fetch(`http://127.0.0.1:${readyPort}/ready`, { signal: readySignal })).ok) break } catch { readySignal.throwIfAborted() }
        await delay(100, undefined, { signal: readySignal })
      }
      phase = 'required project authorization verification'
      // Verify through a separate loopback listener before adopting the new
      // handler on the public dev URL. The existing session's JWTs stay valid.
      const candidate = createServer(nextConfig, verify, upstreamPort, readyPort, () => {})
      try {
        await new Promise((resolve, reject) => { candidate.once('error', reject); candidate.listen(0, '127.0.0.1', resolve) })
        await verifyProject(`http://127.0.0.1:${candidate.address().port}`, identities, project, AbortSignal.any([controller.signal, AbortSignal.timeout(30000)]))
      } finally { candidate.closeAllConnections(); await new Promise(resolve => candidate.close(resolve)) }
      controller.signal.throwIfAborted()
      phase = 'writing generated types'
      await atomicFile(options.output, generate(snapshot), 0o644)
      await acceptContract(snapshot)
      Object.assign(config, nextConfig)
      gateway = candidate.listeners('request')[0]
      await writeReceipt('ready', false)
      await checkClient()
      await writeReceipt()
      event('reloaded', 'Schema refreshed, types regenerated, and RLS verified' + (options.check ? '; client checks passed.' : '.'))
    }
    const report = async (status, error) => {
      if (status === 'source_unavailable') pause()
      await writeReceipt(status, false)
      const diagnostic = phase.startsWith('client ') ? 'Fix client code or dependencies; run the configured check commands for diagnostics.' : migrationDiagnostic(error)
      event(status, status === 'source_unavailable' ? 'Cannot read migration sources or the accepted-contracts file; restore the missing files to retry.' : `${phase}: ${diagnostic} ${gateway ? 'The validated API remains available.' : 'The gateway stays paused until migration files are fixed.'}`)
    }
    if (!options.once && options.watch !== false) {
      if (!options.json) console.log('Watching the configured migrations; add a new migration to refresh the API and types.')
      await watchMigrations({ directory: migrationDirectory, initial: initialSource, source: watchedSource, signal: controller.signal, reload, report })
    } else if (!options.once) await new Promise(resolve => {
      if (controller.signal.aborted) resolve()
      else controller.signal.addEventListener('abort', resolve, { once: true })
    })
  } catch (error) {
    if (stopped && options.replay) throw new DevFailure('Saved request replay interrupted; verification did not complete.')
    if (!stopped) throw error instanceof DevFailure ? error : new DevFailure(`Local Data API failed during ${phase}; check local Docker, numbered SQL migrations, schema permissions, output path and client dependencies.`)
  } finally {
    clearTimeout(startup)
    process.removeListener('SIGINT', stop); process.removeListener('SIGTERM', stop)
    if (server) { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)) }
    // --volumes removes PostgreSQL's anonymous volume as well as the owned containers.
    for (const name of servicesAttempted ? [rest, database] : []) {
      try {
        const existing = await docker(['container', 'ls', '--all', '--filter', `name=^/${name}$`, '--format', '{{.Names}}'], true)
        if (existing === name) await docker(['rm', '--force', '--volumes', name], true)
      } catch { cleanupFailed = true; console.error(`Cleanup failed; remove the owned container with: docker rm --force --volumes ${name}`) }
    }
    if (receiptWritten) await unlink(receipt)
    for (const kind of ['postgres', 'postgrest']) await unlink(join(import.meta.dirname, `${kind}-${suffix}.env`)).catch(() => {})
    if (cleanupFailed) throw new DevFailure('Disposable container cleanup did not complete')
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try { await run(JSON.parse(await readFile(process.argv[2], 'utf8'))) }
  catch (error) { console.error(error instanceof DevFailure ? error.message : 'Local Data API failed: check Node.js 22+ and the project options.'); process.exitCode = 1 }
}
