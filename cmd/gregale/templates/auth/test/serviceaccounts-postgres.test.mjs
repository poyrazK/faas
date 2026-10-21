// ADR-1190 service accounts against PostgreSQL: the organization API, the
// token endpoint and authorize() over sql/0021.
import test from 'node:test'
import assert from 'node:assert/strict'
import { generateKeyPairSync, randomUUID } from 'node:crypto'
import pg from 'pg'
import { signingKey } from '../admin.mjs'
import { route, sqlError } from '../authz.mjs'
import { limits } from '../config.mjs'
import { applyGregaleMigrations } from '../migrate.mjs'
import { adminRoute, issueToken, verificationKeys, verifyToken } from '../serviceaccounts.mjs'

const database = process.env.GREGALE_AUTH_SERVICE_ACCOUNTS_TEST_DATABASE_URL
test('service accounts against PostgreSQL', { skip: !database }, async t => {
  assert.match(new URL(database).pathname, /^\/service_accounts_auth_[a-z0-9_]+$/, 'a dedicated disposable test database is required')
  const pool = new pg.Pool({ connectionString: database, max: 8 })
  t.after(() => pool.end())
  await pool.query(`DROP SCHEMA IF EXISTS gregale_auth CASCADE; DROP SCHEMA IF EXISTS auth CASCADE;
    CREATE SCHEMA auth; CREATE SCHEMA gregale_auth;
    CREATE TABLE auth.users(id uuid PRIMARY KEY,email text,email_confirmed_at timestamptz,deleted_at timestamptz,banned_until timestamptz,is_anonymous boolean NOT NULL DEFAULT false,is_sso_user boolean NOT NULL DEFAULT false,role varchar(255));
    CREATE TABLE auth.sessions(id uuid PRIMARY KEY,user_id uuid REFERENCES auth.users(id),not_after timestamptz,aal text);
    CREATE TABLE auth.mfa_factors(id uuid PRIMARY KEY,user_id uuid REFERENCES auth.users(id),status text,factor_type text);
    CREATE TABLE auth.audit_log_entries(id uuid PRIMARY KEY,created_at timestamptz,payload jsonb);
    CREATE TABLE auth.sso_providers(id uuid PRIMARY KEY);
    CREATE TABLE auth.identities(id uuid PRIMARY KEY,user_id uuid REFERENCES auth.users(id),provider text);
    CREATE TABLE auth.sso_domains(id uuid PRIMARY KEY,sso_provider_id uuid REFERENCES auth.sso_providers(id),domain text);`)
  // Admins hand out only roles within their own permissions (sql/0022), so
  // this app gives its admins the docs permissions too.
  const authz = { roles: { admin: ['members:manage', 'org:read', 'docs:read', 'docs:write'], deployer: ['docs:read', 'docs:write'], viewer: ['docs:read'], auditor: ['audit:read'] },
    policies: [{ action: 'read', resource: 'doc', permission: 'docs:read' }, { action: 'write', resource: 'doc', permission: 'docs:write' }, { action: 'pay', resource: 'invoice', permission: 'billing:pay' }] }
  const client = await pool.connect()
  try { await applyGregaleMigrations(client, undefined, JSON.stringify(authz)) } finally { client.release() }

  const keys = JSON.stringify([{ ...generateKeyPairSync('ec', { namedCurve: 'P-256' }).privateKey.export({ format: 'jwk' }), alg: 'ES256', kid: 'gregale-0a1b2c3d', key_ops: ['sign', 'verify'] }])
  const issuer = 'https://shop-auth.example.test/auth/v1'
  async function person(role, org) {
    const value = { sub: randomUUID(), session_id: randomUUID() }
    await pool.query('INSERT INTO auth.users(id,email,email_confirmed_at) VALUES($1,$2,now())', [value.sub, `${value.sub}@example.test`])
    await pool.query('INSERT INTO auth.sessions(id,user_id) VALUES($1,$2)', [value.session_id, value.sub])
    if (org) await pool.query('INSERT INTO gregale_auth.memberships(org_id,user_id,role) VALUES($1,$2,$3)', [org, value.sub, role])
    return value
  }
  const call = (claims, method, path, body) => route(pool, claims, method, path, body, { serviceAccounts: true }).catch(err => { throw sqlError(err) })
  const owner = await person()
  const [, org] = await call(owner, 'POST', '/orgs', { slug: `acme-${randomUUID().slice(0, 8)}`, name: 'Acme' })
  const [, other] = await call(owner, 'POST', '/orgs', { slug: `other-${randomUUID().slice(0, 8)}`, name: 'Other' })
  const admin = await person('admin', org.id), member = await person('member', org.id)
  const base = `/orgs/${org.id}/service-accounts`
  const token = async (account, secret, scope = null) => {
    const open = { take: () => 0, check: () => 0 }
    const reply = await issueToken({ pool, signer: signingKey(keys), issuer, limiter: open, failures: open }, { clientId: account, secret, scope, challenge: {}, address: '203.0.113.7' })
    return verifyToken(reply.access_token, { keys: verificationKeys(keys), issuer })
  }
  const allowed = async (claims, action, resource) => (await call(claims, 'POST', '/authorize', { action, resource }))[1].allowed
  const doc = (where = org) => `org/${where.id}/doc/readme`

  await t.test('members holding members:manage manage service accounts; only owners make owner-role ones', async () => {
    await assert.rejects(call(member, 'GET', base), { code: 'forbidden' })
    await assert.rejects(call(member, 'POST', base, { name: 'Sneaky', role: 'member' }), { code: 'forbidden' })
    await assert.rejects(call(admin, 'POST', base, { name: 'Root', role: 'owner' }), { code: 'forbidden' })
    await assert.rejects(call(admin, 'POST', base, { name: 'Nope', role: 'ghost' }), { code: 'unknown_role' })
    const [status, made] = await call(admin, 'POST', base, { name: 'CI deploys', role: 'deployer' })
    assert.equal(status, 201)
    const [, listed] = await call(admin, 'GET', base)
    assert.deepEqual(listed.service_accounts.map(a => [a.id, a.name, a.role, a.disabled, a.secrets.length, a.secrets[0].hint]), [[made.client_id, 'CI deploys', 'deployer', false, 1, made.client_secret.slice(-4)]])
    // Service accounts are not members.
    const [, members] = await call(admin, 'GET', `/orgs/${org.id}/members`)
    assert.equal(members.members.some(m => m.user_id === made.client_id), false)
    const [, root] = await call(owner, 'POST', base, { name: 'Root', role: 'owner' })
    await assert.rejects(call(admin, 'POST', `${base}/${root.client_id}/secrets`, {}), { code: 'forbidden' })
    await assert.rejects(call(admin, 'PATCH', `${base}/${root.client_id}`, { name: 'Mine now' }), { code: 'forbidden' })
    await assert.rejects(call(admin, 'PATCH', `${base}/${made.client_id}`, { role: 'owner' }), { code: 'forbidden' })
    await assert.rejects(call(admin, 'DELETE', `${base}/${root.client_id}`), { code: 'forbidden' })
    // Another organization's account is not found through this one.
    await assert.rejects(call(owner, 'DELETE', `/orgs/${other.id}/service-accounts/${made.client_id}`), { code: 'not_found' })
    assert.deepEqual(await call(owner, 'DELETE', `${base}/${root.client_id}`), [204, null])
  })

  await t.test('a token holds its role\'s permissions in its own organization, within its scope', async () => {
    const [, made] = await call(admin, 'POST', base, { name: 'Writer', role: 'deployer' })
    const claims = await token(made.client_id, made.client_secret)
    assert.equal(claims.org_id, org.id)
    assert.deepEqual(claims.org_permissions, ['docs:read', 'docs:write'])
    assert.equal(await allowed(claims, 'read', doc()), true)
    assert.equal(await allowed(claims, 'write', doc()), true)
    assert.equal(await allowed(claims, 'pay', `org/${org.id}/invoice/1`), false)
    assert.equal(await allowed(claims, 'read', doc(other)), false)
    const narrowed = await token(made.client_id, made.client_secret, ['docs:read'])
    assert.equal(await allowed(narrowed, 'read', doc()), true)
    assert.equal(await allowed(narrowed, 'write', doc()), false)
    await assert.rejects(token(made.client_id, made.client_secret, ['billing:pay']), { error: 'invalid_scope' })
    // The helpers never mix the two kinds of principal.
    const one = async (claims, sql) => {
      const connection = await pool.connect()
      try {
        await connection.query('BEGIN')
        await connection.query(`SELECT set_config('request.jwt.claims', $1, true)`, [JSON.stringify(claims)])
        return (await connection.query(sql)).rows[0].value
      } finally { await connection.query('ROLLBACK'); connection.release() }
    }
    assert.equal(await one(claims, 'SELECT gregale_auth.session_active() AS value'), false)
    assert.equal(await one(claims, 'SELECT gregale_auth.service_account_active() AS value'), true)
    assert.equal(await one(owner, 'SELECT gregale_auth.service_account_active() AS value'), false)
    assert.equal(await one({ ...owner, service_account: true, secret_id: claims.secret_id }, 'SELECT gregale_auth.service_account_active() AS value'), false)
    // A person keeps their own permissions.
    assert.equal(await allowed(owner, 'pay', `org/${org.id}/invoice/1`), true)
    assert.equal(await allowed(member, 'read', doc()), false)
    // The token reaches nothing else here.
    await assert.rejects(call(claims, 'GET', base), { code: 'service_account_token' })
  })

  await t.test('revoking, disabling and deleting take effect at the next check', async () => {
    const [, made] = await call(admin, 'POST', base, { name: 'Rotating', role: 'viewer' })
    const first = await token(made.client_id, made.client_secret)
    const [, second] = await call(admin, 'POST', `${base}/${made.client_id}/secrets`, { expires_in_days: 30 })
    assert.ok(new Date(second.secret.expires_at) > new Date())
    await assert.rejects(call(admin, 'POST', `${base}/${made.client_id}/secrets`, {}), { code: 'secret_limit', status: 409 })
    const next = await token(made.client_id, second.client_secret)
    await call(admin, 'DELETE', `${base}/${made.client_id}/secrets/${first.secret_id}`)
    assert.equal(await allowed(first, 'read', doc()), false)
    await assert.rejects(token(made.client_id, made.client_secret), { error: 'invalid_client' })
    assert.equal(await allowed(next, 'read', doc()), true)
    await call(admin, 'PATCH', `${base}/${made.client_id}`, { disabled: true })
    assert.equal(await allowed(next, 'read', doc()), false)
    await assert.rejects(token(made.client_id, second.client_secret), { error: 'invalid_client' })
    await call(admin, 'PATCH', `${base}/${made.client_id}`, { disabled: false, role: 'deployer' })
    assert.equal(await allowed(next, 'write', doc()), true)
    // Role changes apply to tokens already issued, as for members.
    await call(admin, 'PATCH', `${base}/${made.client_id}`, { role: 'viewer' })
    assert.equal(await allowed(next, 'write', doc()), false)
    await call(admin, 'DELETE', `${base}/${made.client_id}`)
    assert.equal(await allowed(next, 'read', doc()), false)
    const expired = await pool.query(`UPDATE gregale_auth.service_account_secrets SET expires_at = created_at + interval '1 microsecond' WHERE service_account_id = $1 RETURNING id`, [made.client_id])
    assert.equal(expired.rowCount, 0)
  })

  await t.test('the operator manages any organization\'s accounts, without an actor', async () => {
    const rows = async (sql, args) => { try { return (await pool.query(sql, args)).rows } catch (err) { throw sqlError(err) } }
    const [, made] = await adminRoute(rows, 'POST', [], new URLSearchParams(), { org: other.id, name: 'Operator made', role: 'owner' })
    const claims = await token(made.client_id, made.client_secret)
    assert.equal(await allowed(claims, 'pay', `org/${other.id}/invoice/1`), true)
    const [, listed] = await adminRoute(rows, 'GET', [], new URLSearchParams(`org=${other.id}`))
    assert.deepEqual(listed.service_accounts.map(a => a.id), [made.client_id])
    await adminRoute(rows, 'DELETE', [made.client_id, 'secrets', claims.secret_id], new URLSearchParams())
    assert.equal(await allowed(claims, 'pay', `org/${other.id}/invoice/1`), false)
    await assert.rejects(adminRoute(rows, 'DELETE', [made.client_id, 'secrets', claims.secret_id], new URLSearchParams()), { code: 'not_found' })
    const log = (await pool.query(`SELECT action, actor_id FROM gregale_auth.org_audit_log WHERE org_id = $1 AND action LIKE 'service_account.%' ORDER BY occurred_at`, [other.id])).rows
    assert.deepEqual(log, [{ action: 'service_account.created', actor_id: null }, { action: 'service_account.secret_created', actor_id: null }, { action: 'service_account.secret_revoked', actor_id: null }])
  })

  await t.test('the organization\'s audit log records who changed what', async () => {
    const log = (await pool.query(`SELECT action, actor_id, data FROM gregale_auth.org_audit_log WHERE org_id = $1 AND action LIKE 'service_account.%' ORDER BY occurred_at`, [org.id])).rows
    const actions = log.map(entry => entry.action)
    for (const action of ['service_account.created', 'service_account.secret_created', 'service_account.updated', 'service_account.secret_revoked', 'service_account.deleted']) assert.ok(actions.includes(action), action)
    assert.ok(log.filter(entry => entry.action === 'service_account.created').every(entry => [owner.sub, admin.sub].includes(entry.actor_id)))
    const disabled = log.find(entry => entry.action === 'service_account.updated' && entry.data.disabled === true)
    assert.equal(disabled.actor_id, admin.sub)
    // Disable, enable with a new role, and a role change; tokens' last_used_at
    // writes are not changes.
    assert.equal(log.filter(entry => entry.action === 'service_account.updated').length, 3)
  })

  await t.test('a role held by a service account cannot be removed, and an organization holds at most the limit', async () => {
    const holder = await pool.connect()
    try {
      await assert.rejects(applyGregaleMigrations(holder, undefined, JSON.stringify({ roles: { viewer: ['docs:read'], auditor: ['audit:read'] }, policies: authz.policies })), { message: 'role_in_use' })
    } finally { holder.release() }
    const [, crowded] = await call(owner, 'POST', '/orgs', { slug: `crowd-${randomUUID().slice(0, 8)}`, name: 'Crowd' })
    const path = `/orgs/${crowded.id}/service-accounts`
    for (let i = 0; i < limits.serviceAccountsPerOrg; i++) await call(owner, 'POST', path, { name: `bot ${i}`, role: 'member' })
    await assert.rejects(call(owner, 'POST', path, { name: 'one too many', role: 'member' }), { code: 'service_account_limit', status: 409 })
    // Deleting the organization takes its accounts with it.
    await pool.query('DELETE FROM gregale_auth.organizations WHERE id = $1', [crowded.id])
    assert.equal((await pool.query('SELECT count(*)::int AS n FROM gregale_auth.service_accounts WHERE org_id = $1', [crowded.id])).rows[0].n, 0)
  })
})
