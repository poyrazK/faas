import { randomBytes } from 'node:crypto'
import { readFile, writeFile, mkdir, mkdtemp, cp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

import { Canary, CanaryFailure, httpsOrigin, requireValue } from '../../data-api/staging/canary.mjs'

// Native acceptance for application Auth (ADR-1190): the Auth app, a Data API
// bound to it and a migration release are built by Gregale's remote builder,
// then sign-up, row-level security, session revocation, owner administration,
// import, parking and wake are exercised through the public edge. It reuses
// the Data API canary's manifest, provenance, evidence and cleanup rules.

export function validateConfig(raw, env) {
  requireValue(raw.environment === 'staging', 'staging_designation_required')
  const api = httpsOrigin(raw.api)
  requireValue(!env.FAAS_API || httpsOrigin(env.FAAS_API) === api, 'staging_api_mismatch')
  requireValue(typeof raw.account_id === 'string' && raw.account_id.length > 0, 'staging_account_required')
  requireValue(typeof raw.region === 'string' && /^[a-z0-9-]+$/.test(raw.region), 'staging_region_required')
  requireValue(Array.isArray(raw.builder_node_ids) && raw.builder_node_ids.length > 0 && raw.builder_node_ids.every(x => typeof x === 'string' && x.length > 0), 'native_builder_allowlist_required')
  requireValue(typeof raw.app_host_suffix === 'string' && /^\.[a-z0-9.-]+$/.test(raw.app_host_suffix), 'staging_app_host_suffix_required')
  requireValue(Boolean(env.FAAS_TOKEN), 'staging_token_required')
  requireValue(/^[a-f0-9]{40}$/.test(env.FAAS_AUTH_SOURCE_COMMIT || ''), 'source_commit_required')
  return { ...raw, api, token: env.FAAS_TOKEN, sourceCommit: env.FAAS_AUTH_SOURCE_COMMIT }
}

const claims = token => JSON.parse(Buffer.from(String(token).split('.')[1] ?? '', 'base64url').toString('utf8') || 'null')
const header = token => JSON.parse(Buffer.from(String(token).split('.')[0] ?? '', 'base64url').toString('utf8') || 'null')

export class AuthCanary extends Canary {
  constructor(config, options) {
    super(config, options)
    this.prefix = `auth-${randomBytes(6).toString('hex')}`
    // Passwords, tokens, keys and addresses stay in memory, never in evidence.
    this.password = randomBytes(24).toString('base64url')
    Object.assign(this.report, { scope: 'auth-staging-canary', run_id: this.prefix, plan: {}, storage: null })
  }

  // json calls the public edge and returns status and parsed body only.
  async json(url, { method = 'GET', token, body, headers = {} } = {}) {
    const response = await this.publicFetch(url, {
      method, body: body === undefined ? undefined : JSON.stringify(body),
      headers: { Accept: 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}), ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}), ...headers },
    }).catch(() => { throw new CanaryFailure('public_request_failed') })
    const text = await response.text()
    let data = null
    try { data = text ? JSON.parse(text) : null } catch { data = null }
    return { status: response.status, data }
  }

  email(name) { return `${this.prefix}-${name}@example.com` }

  async signUp(authURL, name) {
    const { status, data } = await this.json(authURL + '/auth/v1/signup', { method: 'POST', body: { email: this.email(name), password: this.password } })
    requireValue(status === 200 && typeof data?.access_token === 'string' && typeof data?.refresh_token === 'string' && data.user?.id, 'signup_failed')
    return { id: data.user.id, access: data.access_token, refresh: data.refresh_token }
  }

  notes(apiURL, token, init = {}) {
    return this.json(apiURL + '/rest/v1/notes' + (init.query ?? '?select=id,owner,body'), { ...init, token, headers: { 'Accept-Profile': 'api', 'Content-Profile': 'api', Prefer: 'return=representation' } })
  }

  async run() {
    this.workspace = await mkdtemp(join(tmpdir(), 'gregale-auth-canary-'))
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
      const auth = await this.createApp('auth')
      const api = await this.createApp('api')
      const authURL = this.appURL(auth), apiURL = this.appURL(api)

      await this.check('auth_native_build', async () => this.deployed(await this.exec(['auth', 'create', auth.slug, '--database', db.id, '--site-url', apiURL, '--autoconfirm', '--resume']), auth))
      const jwks = await this.check('auth_ready_with_es256_jwks', async () => {
        await this.wait(async () => (await this.json(authURL + '/healthz')).status, status => status === 200, 300_000)
        const { status, data } = await this.json(authURL + '/auth/v1/.well-known/jwks.json')
        requireValue(status === 200 && Array.isArray(data?.keys) && data.keys.length > 0 && data.keys.every(k => k.kty === 'EC' && k.crv === 'P-256' && !k.d), 'jwks_invalid')
        return data.keys
      })

      const migrator = await this.createApp('migrate')
      await this.check('migration_binding_ready', () => this.attachMigration(db.id, migrator))
      await this.request(`/v1/apps/${migrator.slug}`, { method: 'PATCH', body: { egress_ports: [5432] } })
      const migrationDir = join(this.workspace, 'migration'); await mkdir(migrationDir)
      for (const file of ['package.json', 'package-lock.json']) await cp(join(this.repoRoot, 'cmd/gregale/templates/data-api', file), join(migrationDir, file))
      await cp(join(this.repoRoot, 'tests/auth/staging/migrate.mjs'), join(migrationDir, 'migrate.mjs'))
      await cp(join(this.repoRoot, 'tests/data-api/staging/migration-server.mjs'), join(migrationDir, 'migration-server.mjs'))
      await writeFile(join(migrationDir, 'gregale.yaml'), 'release:\n  command: node migrate.mjs\n')
      await writeFile(join(migrationDir, 'Dockerfile'), 'FROM node:22-bookworm-slim\nWORKDIR /app\nCOPY package*.json ./\nRUN npm ci --omit=dev --ignore-scripts\nCOPY . .\nEXPOSE 8080\nCMD ["node","migration-server.mjs"]\n')
      await this.check('rls_migration_native_build', () => this.deploy(migrator, migrationDir))
      await this.check('data_api_native_build', async () => this.deployed(await this.exec(['data-api', 'create', api.slug, '--database', db.id, '--auth', auth.slug, '--resume']), api))

      const alice = await this.check('signup_issues_verifiable_tokens', async () => {
        const user = await this.signUp(authURL, 'alice')
        const head = header(user.access), body = claims(user.access)
        requireValue(head?.alg === 'ES256' && jwks.some(k => k.kid === head.kid), 'token_not_signed_by_published_key')
        requireValue(body?.iss === authURL + '/auth/v1' && body.aud === 'authenticated' && body.sub === user.id && typeof body.session_id === 'string' && body.exp - body.iat === 600, 'token_claims_invalid')
        return user
      })
      const bob = await this.signUp(authURL, 'bob')

      await this.check('data_api_rls_isolation', async () => {
        const created = await this.notes(apiURL, alice.access, { method: 'POST', body: { body: 'alice-only' } })
        requireValue(created.status === 201 && created.data?.[0]?.owner === alice.id, 'owner_insert_failed')
        const own = await this.notes(apiURL, alice.access)
        requireValue(own.status === 200 && own.data?.length === 1, 'owner_read_failed')
        const other = await this.notes(apiURL, bob.access)
        requireValue(other.status === 200 && other.data?.length === 0, 'cross_user_read_allowed')
        const forged = await this.notes(apiURL, bob.access, { method: 'POST', body: { owner: alice.id, body: 'forged' } })
        requireValue(forged.status >= 400, 'cross_user_write_allowed')
      })

      await this.check('global_sign_out_revokes_at_next_request', async () => {
        requireValue((await this.json(authURL + '/auth/v1/logout?scope=global', { method: 'POST', token: alice.access })).status === 204, 'sign_out_failed')
        // The access token is still unexpired; row-level security must not honour it.
        const rows = await this.notes(apiURL, alice.access)
        requireValue(rows.status === 401 || (rows.status === 200 && rows.data?.length === 0), 'revoked_session_read_allowed')
        requireValue((await this.notes(apiURL, alice.access, { method: 'POST', body: { body: 'after sign-out' } })).status >= 400, 'revoked_session_write_allowed')
        requireValue((await this.json(authURL + '/auth/v1/token?grant_type=refresh_token', { method: 'POST', body: { refresh_token: alice.refresh } })).status >= 400, 'revoked_refresh_allowed')
      })

      const adminKey = await this.check('owner_admin_key', async () => {
        const created = await this.exec(['auth', 'admin-key', 'create', auth.slug])
        requireValue(/^gaak_[A-Za-z0-9_-]{43}$/.test(created.key ?? '') && /^[0-9A-F]{8}$/.test(created.id ?? ''), 'admin_key_receipt_invalid')
        // The key is live once the fresh restart has loaded it.
        const listed = await this.wait(() => this.json(authURL + `/admin/v1/users?search=${this.prefix}`, { token: created.key }), x => x.status === 200, 300_000)
        requireValue(listed.data?.total === 2 && listed.data.users.every(u => !('user_metadata' in u)), 'admin_list_invalid')
        requireValue((await this.json(authURL + '/auth/v1/admin/users', { token: created.key })).status === 404, 'public_gotrue_admin_open')
        return created.key
      })

      await this.check('user_import', async () => {
        const file = join(this.workspace, 'users.jsonl')
        await writeFile(file, JSON.stringify({ email: this.email('dave'), email_confirmed: true, user_metadata: { source: 'canary' } }) + '\n', { mode: 0o600 })
        const result = await this.exec(['auth', 'import', auth.slug, '--file', file, '--admin-key-stdin'], this.workspace, adminKey)
        requireValue(result.created === 1 && result.skipped?.length === 0, 'import_failed')
        const listed = await this.json(authURL + `/admin/v1/users?search=${this.prefix}`, { token: adminKey })
        requireValue(listed.status === 200 && listed.data?.total === 3, 'imported_user_missing')
      })

      await this.check('admin_ban_revokes_at_next_request', async () => {
        const banned = await this.json(authURL + `/admin/v1/users/${bob.id}/ban`, { method: 'POST', token: adminKey, body: { duration: '24h' } })
        requireValue(banned.status === 200 && banned.data?.user?.banned_until, 'ban_failed')
        const rows = await this.notes(apiURL, bob.access)
        requireValue(rows.status === 401 || (rows.status === 200 && rows.data?.length === 0), 'banned_user_read_allowed')
        requireValue((await this.json(authURL + '/auth/v1/token?grant_type=password', { method: 'POST', body: { email: this.email('bob'), password: this.password } })).status >= 400, 'banned_user_signed_in')
      })

      await this.check('signing_key_rotation', async () => {
        // Carol signs in under the first key; her token must outlive the rotation.
        const carol = await this.signUp(authURL, 'carol')
        const rotated = await this.exec(['auth', 'keys', 'rotate', auth.slug])
        requireValue(/^[0-9A-F]{8}$/.test(rotated.id ?? '') && /^[0-9A-F]{8}$/.test(rotated.previous ?? '') && header(carol.access)?.kid === `gregale-${rotated.previous.toLowerCase()}`, 'rotation_receipt_invalid')
        requireValue((await this.notes(apiURL, carol.access)).status === 200, 'previous_key_token_refused')
        const fresh = await this.json(authURL + '/auth/v1/token?grant_type=password', { method: 'POST', body: { email: this.email('carol'), password: this.password } })
        requireValue(fresh.status === 200 && header(fresh.data?.access_token)?.kid === rotated.kid, 'new_key_not_signing')
        requireValue((await this.notes(apiURL, fresh.data.access_token)).status === 200, 'new_key_token_refused')
        // Revoking the previous key cuts its unexpired tokens off: in the Auth
        // app once it restarts, and in the Data API when its JWKS cache expires.
        await this.exec(['auth', 'keys', 'revoke', auth.slug, rotated.previous])
        await this.wait(() => this.json(authURL + '/auth/v1/user', { token: carol.access }), x => x.status === 401 || x.status === 403, 300_000)
        await this.wait(() => this.notes(apiURL, carol.access), x => x.status === 401, 420_000)
        this.report.key_rotation = { signing: rotated.id, revoked: rotated.previous }
        await this.save()
      })

      await this.check('park_and_wake_sign_in', async () => {
        const deployment = this.report.deployments.find(x => x.app === auth.slug)?.deployment_id
        const parked = await this.wait(() => this.request(`/v1/apps/${auth.slug}/instances`), xs => xs.some(x => x.state === 'PARKED' && x.deployment_id === deployment), 900_000)
        const record = instances => instances.filter(x => x.deployment_id === deployment && ['PARKED', 'RUNNING'].includes(x.state)).map(x => ({ id: x.id, deployment_id: x.deployment_id, wake_id: x.wake_id, state: x.state, ram_mb: x.ram_mb }))
        this.report.instances.push(...record(parked)); await this.save()
        const start = Date.now()
        const signedIn = await this.json(authURL + '/auth/v1/token?grant_type=password', { method: 'POST', body: { email: this.email('alice'), password: this.password } })
        this.report.wake_duration_ms = Date.now() - start
        requireValue(signedIn.status === 200 && signedIn.data?.access_token, 'wake_sign_in_failed')
        const rows = await this.notes(apiURL, signedIn.data.access_token)
        requireValue(rows.status === 200 && rows.data?.length === 1, 'new_session_lost_rows')
        const running = await this.wait(() => this.request(`/v1/apps/${auth.slug}/instances`), xs => xs.some(x => x.state === 'RUNNING' && x.deployment_id === deployment))
        this.report.instances.push(...record(running)); await this.save()
      })

      await this.check('plan_memory_and_storage', async () => {
        const app = await this.request(`/v1/apps/${auth.slug}`)
        const instances = await this.request(`/v1/apps/${auth.slug}/instances`)
        requireValue(Number.isInteger(app.ram_mb) && app.ram_mb > 0, 'app_ram_missing')
        requireValue(instances.every(x => x.state !== 'FAILED' && (!x.ram_mb || x.ram_mb <= app.ram_mb)), 'auth_instance_failed_or_oversized')
        this.report.plan = { ram_mb: app.ram_mb, instances: instances.length }
        // The storage rollup is daily; record it when it has run.
        const usage = await this.request(`/v1/usage/storage?day=${new Date().toISOString().slice(0, 10)}`).catch(() => null)
        const row = usage?.items?.find(x => x.app_id === auth.id)
        this.report.storage = row ? { snapshot_bytes: row.snapshot_bytes, layer_bytes: row.layer_bytes } : null
        await this.save()
      })
      this.report.result = 'passed'
    } catch (error) {
      this.report.result = 'failed'
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
    const config = validateConfig(JSON.parse(await readFile(process.env.FAAS_AUTH_STAGING_CONFIG, 'utf8')), process.env)
    if (process.argv.includes('--check-config')) console.log('Auth staging configuration passed; no provider calls made')
    else {
      const interrupt = new AbortController()
      process.once('SIGINT', () => interrupt.abort())
      process.once('SIGTERM', () => interrupt.abort())
      const runner = new AuthCanary(config, { evidence: resolve(process.env.FAAS_AUTH_EVIDENCE || 'auth-staging-evidence.json'), cli: process.env.GREGALE_CANARY_BIN, repoRoot: resolve(new URL('../../../', import.meta.url).pathname), signal: interrupt.signal })
      const report = await runner.run()
      console.log(`Auth staging canary passed ${report.checks.length} checks; cleanup passed`)
    }
  } catch (error) {
    const code = error instanceof CanaryFailure ? error.code : 'configuration_or_canary_failure'
    console.error(`Auth staging canary failed (${code}); inspect configuration and evidence when present`)
    process.exitCode = 1
  }
}
