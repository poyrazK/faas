import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { AuthCanary, validateConfig } from './canary.mjs'

const raw = { environment: 'staging', api: 'https://staging.example', account_id: 'canary-account', region: 'eu', builder_node_ids: ['native-builder'], app_host_suffix: '.apps.example' }
const env = { FAAS_TOKEN: 'private-test-token', FAAS_AUTH_SOURCE_COMMIT: 'b'.repeat(40) }
const repoRoot = resolve(new URL('../../../', import.meta.url).pathname)

test('preflight requires an explicit staging target, native builders and a source commit', () => {
  assert.equal(validateConfig(raw, env).api, raw.api)
  for (const change of [{ environment: 'production' }, { account_id: '' }, { builder_node_ids: [] }, { api: 'http://staging.example' }, { app_host_suffix: '' }, { region: '../prod' }]) {
    assert.throws(() => validateConfig({ ...raw, ...change }, env), undefined, JSON.stringify(change))
  }
  assert.throws(() => validateConfig(raw, { ...env, FAAS_API: 'https://other.example' }), /staging_api_mismatch/)
  assert.throws(() => validateConfig(raw, { ...env, FAAS_AUTH_SOURCE_COMMIT: '' }), /source_commit_required/)
})

const sha = 'c'.repeat(64)
const encode = value => Buffer.from(JSON.stringify(value)).toString('base64url')

// world simulates the staging control plane, CLI and public edge.
function world({ breakIsolation = false } = {}) {
  const apps = new Map(), deleted = []
  const users = new Map(), notes = [], sessions = new Map()
  let adminCalls = 0, sessionSeq = 0, parkedSeen = false, woke = false, signing = '0A1B2C3D'
  const revoked = new Set()
  const kid = id => `gregale-${id.toLowerCase()}`
  const json = (status, body) => new Response(body === undefined ? null : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
  const issue = (iss, user) => {
    const session = `session-${++sessionSeq}`
    sessions.set(session, user.id)
    const iat = Math.floor(Date.now() / 1000)
    return { access_token: `${encode({ alg: 'ES256', kid: kid(signing) })}.${encode({ iss, aud: 'authenticated', sub: user.id, session_id: session, iat, exp: iat + 600 })}.sig`, refresh_token: `refresh:${session}`, user: { id: user.id } }
  }
  const caller = init => {
    const token = /^Bearer (.+)$/.exec(init?.headers?.Authorization ?? '')?.[1]
    if (!token || !token.includes('.')) return null
    if (revoked.has(JSON.parse(Buffer.from(token.split('.')[0], 'base64url')).kid)) return null
    const claims = JSON.parse(Buffer.from(token.split('.')[1], 'base64url'))
    const user = [...users.values()].find(u => u.id === claims.sub)
    return { claims, active: sessions.get(claims.session_id) === claims.sub && !user?.banned }
  }
  const receipt = name => ({ id: `dep-${name}`, status: 'live', build_id: `build-${name}`, source_sha256: sha })
  const cli = async (binary, args, options) => {
    const [, ...rest] = args
    const key = rest.slice(0, 3).join(' ')
    if (rest[0] === 'postgres' && rest[1] === 'create') return JSON.stringify({ id: 'db-1', name: rest[2] })
    if (key === 'postgres get db-1') return JSON.stringify({ state: deleted.includes('db-1') ? 'deleted' : 'ready' })
    if (rest[0] === 'auth' && rest[1] === 'create') return JSON.stringify(receipt('auth'))
    if (rest[0] === 'postgres' && rest[1] === 'attach') return JSON.stringify({ id: 'bind-m', app_id: apps.get(rest[3]).id, access: 'migration' })
    if (rest[0] === 'postgres' && rest[1] === 'bindings' && rest[2] === 'get') return JSON.stringify({ state: deleted.includes(rest[3]) ? 'deleted' : 'ready' })
    if (rest[0] === 'deploy') return JSON.stringify(receipt('migrate'))
    if (rest[0] === 'data-api' && rest[1] === 'create') { assert.ok(rest.includes('--auth')); return JSON.stringify(receipt('api')) }
    if (key.startsWith('auth admin-key create')) return JSON.stringify({ id: '0A1B2C3D', key: 'gaak_' + 'Q'.repeat(43) })
    if (key === 'auth keys rotate') {
      const previous = signing
      signing = 'FFFF0001'
      return JSON.stringify({ id: signing, kid: kid(signing), state: 'signing', previous })
    }
    if (key === 'auth keys revoke') { revoked.add(kid(rest[4])); return JSON.stringify({ id: rest[4], kid: kid(rest[4]), state: 'removing' }) }
    if (rest[0] === 'auth' && rest[1] === 'import') {
      assert.equal(options.stdin, 'gaak_' + 'Q'.repeat(43))
      const record = JSON.parse((await readFile(rest[rest.indexOf('--file') + 1], 'utf8')).trim())
      users.set(record.email, { id: `user-${users.size + 1}`, email: record.email })
      return JSON.stringify({ created: 1, skipped: [], records: 1 })
    }
    if (key === 'postgres bindings list') return JSON.stringify({ items: [...apps.values()].map((app, i) => ({ id: `bind-${i}`, app_id: app.id, state: deleted.includes(`bind-${i}`) ? 'deleted' : 'ready' })) })
    if (key.startsWith('postgres bindings delete')) { deleted.push(rest[3]); return '{}' }
    if (key === 'postgres delete db-1') { deleted.push('db-1'); return '{}' }
    throw new Error('unexpected command ' + rest.join(' '))
  }
  const request = async (path, options = {}) => {
    const method = options.method ?? 'GET'
    if (path === '/v1/account') return { id: 'canary-account' }
    if (path === '/v1/apps' && method === 'POST') {
      const app = { id: `id-${options.body.slug}`, slug: options.body.slug, url: `https://${options.body.slug}.apps.example`, ram_mb: 128 }
      apps.set(app.slug, app)
      return app
    }
    const build = /^\/v1\/builds\/(.+)\/provenance$/.exec(path)
    if (build) return { build_id: build[1], builder_node_id: 'native-builder', source_sha256: sha }
    const instances = /^\/v1\/apps\/([^/]+)\/instances$/.exec(path)
    if (instances) return [{ id: 'i-1', deployment_id: 'dep-auth', wake_id: 'w-1', state: woke ? 'RUNNING' : (parkedSeen = true, 'PARKED'), ram_mb: 128 }]
    if (path.startsWith('/v1/usage/storage')) return { items: [{ app_id: [...apps.values()][0].id, snapshot_bytes: 90e6, layer_bytes: 40e6 }] }
    const app = /^\/v1\/apps\/([^/?]+)$/.exec(path)
    if (app && method === 'DELETE') { deleted.push(app[1]); return null }
    if (app) return apps.get(app[1])
    return {}
  }
  const publicFetch = async (input, init = {}) => {
    const url = new URL(input)
    const body = init.body ? JSON.parse(init.body) : undefined
    const iss = `${url.origin}/auth/v1`
    const route = `${init.method ?? 'GET'} ${url.pathname}`
    if (route === 'GET /healthz') return json(200, { ready: true })
    if (route === 'GET /auth/v1/.well-known/jwks.json') return json(200, { keys: [{ kty: 'EC', crv: 'P-256', kid: kid(signing), x: 'x', y: 'y' }] })
    if (route === 'GET /auth/v1/user') return caller(init) ? json(200, {}) : json(401, {})
    if (route === 'POST /auth/v1/signup') {
      const user = { id: `user-${users.size + 1}`, email: body.email, password: body.password }
      users.set(body.email, user)
      return json(200, issue(iss, user))
    }
    if (route === 'POST /auth/v1/logout') {
      const who = caller(init)
      for (const [session, sub] of sessions) if (sub === who.claims.sub) sessions.delete(session)
      return json(204)
    }
    if (route === 'POST /auth/v1/token') {
      if (url.searchParams.get('grant_type') === 'refresh_token') return sessions.has(body.refresh_token.slice(8)) ? json(200, {}) : json(400, { error_code: 'refresh_token_not_found' })
      const user = users.get(body.email)
      if (!user || user.banned || user.password !== body.password) return json(400, { error_code: 'invalid_credentials' })
      // Parked once observed; the next sign-in wakes it.
      woke = parkedSeen
      return json(200, issue(iss, user))
    }
    if (url.pathname === '/rest/v1/notes') {
      const who = caller(init)
      if (!who) return json(401, {})
      if (init.method === 'POST') {
        const owner = body.owner ?? who.claims.sub
        if (!who.active || (owner !== who.claims.sub && !breakIsolation)) return json(403, { code: '42501' })
        const row = { id: notes.length + 1, owner, body: body.body }
        notes.push(row)
        return json(201, [row])
      }
      return json(200, notes.filter(row => who.active && (breakIsolation || row.owner === who.claims.sub)))
    }
    if (url.pathname.startsWith('/auth/v1/admin')) return json(404, { code: 'not_found' })
    if (url.pathname.startsWith('/admin/v1/')) {
      if (init.headers.Authorization !== 'Bearer gaak_' + 'Q'.repeat(43)) return json(401, {})
      if (++adminCalls === 1) return json(404, { code: 'not_found' }) // not restarted yet
      if (url.pathname === '/admin/v1/users') {
        const matched = [...users.values()].filter(u => u.email.includes(url.searchParams.get('search') ?? ''))
        return json(200, { users: matched.map(u => ({ id: u.id, email: u.email })), total: matched.length })
      }
      const ban = /^\/admin\/v1\/users\/([^/]+)\/ban$/.exec(url.pathname)
      if (ban) {
        const user = [...users.values()].find(u => u.id === ban[1])
        user.banned = true
        return json(200, { user: { id: user.id, banned_until: 'later' } })
      }
    }
    return json(404, {})
  }
  return { cli, request, publicFetch, deleted, apps }
}

async function runner(t, simulated) {
  const directory = await mkdtemp(join(tmpdir(), 'auth-canary-'))
  t.after(() => rm(directory, { recursive: true, force: true }))
  const canary = new AuthCanary(validateConfig(raw, env), { evidence: join(directory, 'evidence.json'), cli: 'gregale', repoRoot, runCommand: simulated.cli, request: simulated.request })
  canary.publicFetch = simulated.publicFetch
  return canary
}

test('the complete journey passes, records evidence without secrets and cleans up', { timeout: 60_000 }, async t => {
  const simulated = world()
  const canary = await runner(t, simulated)
  const report = await canary.run()
  assert.equal(report.result, 'passed')
  assert.deepEqual(report.checks, ['staging_account', 'database_ready', 'auth_native_build', 'auth_ready_with_es256_jwks', 'migration_binding_ready', 'rls_migration_native_build', 'data_api_native_build',
    'signup_issues_verifiable_tokens', 'data_api_rls_isolation', 'global_sign_out_revokes_at_next_request', 'owner_admin_key', 'user_import', 'admin_ban_revokes_at_next_request', 'signing_key_rotation', 'park_and_wake_sign_in', 'plan_memory_and_storage'])
  assert.deepEqual(report.deployments.map(d => d.app), [`${canary.prefix}-auth`, `${canary.prefix}-migrate`, `${canary.prefix}-api`])
  assert.equal(report.plan.ram_mb, 128)
  assert.deepEqual(report.storage, { snapshot_bytes: 90e6, layer_bytes: 40e6 })
  assert.ok(Number.isInteger(report.wake_duration_ms))
  assert.equal(report.cleanup.result, 'passed')
  assert.ok(simulated.deleted.includes('db-1') && simulated.deleted.includes(`${canary.prefix}-auth`))
  const evidence = await readFile(canary.evidence, 'utf8')
  for (const secret of [canary.password, 'gaak_', 'private-test-token', '@example.com', 'refresh:', '.sig']) assert.equal(evidence.includes(secret), false, secret)
})

test('a row-level security leak fails the canary and still cleans up', { timeout: 60_000 }, async t => {
  const simulated = world({ breakIsolation: true })
  const canary = await runner(t, simulated)
  await assert.rejects(canary.run(), /staging_canary_failed/)
  assert.deepEqual(canary.report.failure, { check: 'data_api_rls_isolation', code: 'cross_user_read_allowed' })
  assert.equal(canary.report.cleanup.result, 'passed')
  assert.ok(simulated.deleted.includes('db-1'))
})
