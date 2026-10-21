// ADR-1190 against PostgreSQL: role grants stay within the granter's
// permissions, invitations are rate-limited, and identity providers an
// organization connects itself are marked self-service (sql/0022, 0023).
import test from 'node:test'
import assert from 'node:assert/strict'
import { randomBytes, randomUUID } from 'node:crypto'
import pg from 'pg'
import { route, sqlError } from '../authz.mjs'
import { applyGregaleMigrations } from '../migrate.mjs'
import { newSecret } from '../serviceaccounts.mjs'

const database = process.env.GREGALE_AUTH_GRANTS_TEST_DATABASE_URL
test('role grants, invitation limits and self-service providers against PostgreSQL', { skip: !database }, async t => {
  assert.match(new URL(database).pathname, /^\/grants_auth_[a-z0-9_]+$/, 'a dedicated disposable test database is required')
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
  const authz = { roles: { billing_admin: ['billing:manage', 'org:read'], domain_admin: ['domains:manage', 'sso:manage', 'org:read'], everything: ['*'] },
    policies: [{ action: 'refund', resource: 'invoice', permission: 'billing:manage' }] }
  const client = await pool.connect()
  try {
    for (let i = 0; i < 2; i++) await applyGregaleMigrations(client, undefined, JSON.stringify(authz))
  } finally { client.release() }
  assert.equal((await pool.query('SELECT count(*)::int AS n FROM gregale_auth.schema_versions')).rows[0].n, 23)

  async function person(role, org) {
    const value = { sub: randomUUID(), session_id: randomUUID() }
    await pool.query('INSERT INTO auth.users(id,email,email_confirmed_at) VALUES($1,$2,now())', [value.sub, `${value.sub}@example.test`])
    await pool.query('INSERT INTO auth.sessions(id,user_id) VALUES($1,$2)', [value.session_id, value.sub])
    if (org) await pool.query('INSERT INTO gregale_auth.memberships(org_id,user_id,role) VALUES($1,$2,$3)', [org, value.sub, role])
    return value
  }
  let mailed = 0
  const services = { serviceAccounts: true, invitations: { mailer: { send: async () => { mailed++ } }, external: 'https://shop-auth.example.test', app: 'Shop' } }
  const call = (claims, method, path, body) => route(pool, claims, method, path, body, services).catch(err => { throw sqlError(err) })
  // one runs a statement as a person in one transaction, kept or not.
  const one = async (claims, sql, args, keep = false) => {
    const connection = await pool.connect()
    try {
      await connection.query('BEGIN')
      await connection.query(`SELECT set_config('request.jwt.claims', $1, true)`, [JSON.stringify(claims)])
      const { rows } = await connection.query(sql, args)
      if (keep) await connection.query('COMMIT')
      return rows
    } finally { await connection.query('ROLLBACK').catch(() => {}); connection.release() }
  }
  const owner = await person()
  const [, org] = await call(owner, 'POST', '/orgs', { slug: `grants-${Date.now()}`, name: 'Grants' })
  const admin = await person('admin', org.id), billing = await person('billing_admin', org.id), member = await person('member', org.id)
  const refund = async claims => (await call(claims, 'POST', '/authorize', { action: 'refund', resource: `org/${org.id}/invoice/1` }))[1].allowed

  await t.test('a member gives only roles whose permissions it holds', async () => {
    await assert.rejects(call(admin, 'PATCH', `/orgs/${org.id}/members/${admin.sub}`, { role: 'billing_admin' }), { code: 'forbidden' })
    assert.equal(await refund(admin), false)
    await assert.rejects(call(admin, 'PATCH', `/orgs/${org.id}/members/${member.sub}`, { role: 'billing_admin' }), { code: 'forbidden' })
    await assert.rejects(call(admin, 'PATCH', `/orgs/${org.id}/members/${member.sub}`, { role: 'everything' }), { code: 'forbidden' })
    await assert.rejects(call(admin, 'POST', `/orgs/${org.id}/invitations`, { email: 'x@example.test', role: 'billing_admin' }), { code: 'forbidden' })
    // Nor change or remove someone whose role carries more.
    await assert.rejects(call(admin, 'PATCH', `/orgs/${org.id}/members/${billing.sub}`, { role: 'member' }), { code: 'forbidden' })
    await assert.rejects(call(admin, 'DELETE', `/orgs/${org.id}/members/${billing.sub}`), { code: 'forbidden' })
    // Roles within its own permissions still work, and anyone may leave.
    assert.equal((await call(admin, 'PATCH', `/orgs/${org.id}/members/${member.sub}`, { role: 'admin' }))[1].role, 'admin')
    assert.equal((await call(admin, 'PATCH', `/orgs/${org.id}/members/${member.sub}`, { role: 'member' }))[1].role, 'member')
    assert.equal((await call(owner, 'PATCH', `/orgs/${org.id}/members/${member.sub}`, { role: 'billing_admin' }))[1].role, 'billing_admin')
    assert.equal(await refund(member), true)
    assert.deepEqual(await call(owner, 'DELETE', `/orgs/${org.id}/members/${member.sub}`), [204, null])
    const leaver = await person('billing_admin', org.id)
    assert.deepEqual(await call(leaver, 'DELETE', `/orgs/${org.id}/members/${leaver.sub}`), [204, null])
  })

  await t.test('service accounts, domain joining and identity providers follow the same rule', async () => {
    await assert.rejects(call(admin, 'POST', `/orgs/${org.id}/service-accounts`, { name: 'Refunds', role: 'billing_admin' }), { code: 'forbidden' })
    const [, made] = await call(owner, 'POST', `/orgs/${org.id}/service-accounts`, { name: 'Refunds', role: 'billing_admin' })
    for (const [method, path, body] of [['PATCH', '', { name: 'Mine' }], ['DELETE', ''], ['POST', '/secrets', {}]]) {
      await assert.rejects(call(admin, method, `/orgs/${org.id}/service-accounts/${made.client_id}${path}`, body), { code: 'forbidden' }, `${method} ${path}`)
    }
    const [, ordinary] = await call(admin, 'POST', `/orgs/${org.id}/service-accounts`, { name: 'Reader', role: 'member' })
    await assert.rejects(call(admin, 'PATCH', `/orgs/${org.id}/service-accounts/${ordinary.client_id}`, { role: 'billing_admin' }), { code: 'forbidden' })
    const domains = await person('domain_admin', org.id)
    await pool.query(`INSERT INTO gregale_auth.org_domains(org_id, domain, token, verified_at) VALUES ($1, 'grants.example', $2, now())`, [org.id, randomBytes(24).toString('base64url')])
    await assert.rejects(one(domains, `SELECT * FROM gregale_auth.set_domain_join($1, 'grants.example', 'automatic', 'billing_admin')`, [org.id]), { message: 'forbidden' })
    assert.equal((await one(domains, `SELECT join_role FROM gregale_auth.set_domain_join($1, 'grants.example', 'suggest', 'member')`, [org.id]))[0].join_role, 'member')
    await assert.rejects(one(domains, `SELECT gregale_auth.check_org_sso($1, NULL, ARRAY['grants.example'], 'billing_admin')`, [org.id]), { message: 'forbidden' })
    await one(domains, `SELECT gregale_auth.check_org_sso($1, NULL, ARRAY['grants.example'], 'member')`, [org.id])
  })

  await t.test('invitations: no duplicates, at most 50 an hour per inviter and 10 a day per address', async () => {
    const before = mailed
    await call(owner, 'POST', `/orgs/${org.id}/invitations`, { email: 'victim@example.test', role: 'member' })
    await assert.rejects(call(owner, 'POST', `/orgs/${org.id}/invitations`, { email: 'Victim@example.test', role: 'member' }), { code: 'already_invited', status: 409 })
    // Ten organizations may each invite one address once a day, no more.
    for (let i = 1; i < 10; i++) {
      const [, other] = await call(owner, 'POST', '/orgs', { slug: `other-${i}-${Date.now()}`, name: `Other ${i}` })
      await call(owner, 'POST', `/orgs/${other.id}/invitations`, { email: 'victim@example.test', role: 'member' })
    }
    const [, eleventh] = await call(owner, 'POST', '/orgs', { slug: `eleventh-${Date.now()}`, name: 'Eleventh' })
    await assert.rejects(call(owner, 'POST', `/orgs/${eleventh.id}/invitations`, { email: 'victim@example.test', role: 'member' }), { code: 'invitation_rate_limited', status: 429 })
    // Revoking does not reset the count.
    const { rows } = await pool.query(`SELECT id FROM gregale_auth.invitations WHERE org_id = $1`, [org.id])
    for (const { id } of rows) await call(owner, 'DELETE', `/orgs/${org.id}/invitations/${id}`)
    await assert.rejects(call(owner, 'POST', `/orgs/${org.id}/invitations`, { email: 'victim@example.test', role: 'member' }), { code: 'invitation_rate_limited' })
    // One inviter sends at most 50 an hour, to any addresses.
    const sender = await person('admin', org.id)
    let refused = null
    for (let i = 0; i < 55 && !refused; i++) {
      try { await call(sender, 'POST', `/orgs/${org.id}/invitations`, { email: `person${i}@example.test`, role: 'member' }) } catch (err) { refused = [i, err.code] }
    }
    assert.deepEqual(refused, [50, 'invitation_rate_limited'])
    assert.equal(mailed - before, 10 + 50, 'only accepted invitations were mailed')
  })

  await t.test('a provider the organization connects is self-service; the operator\'s is not', async () => {
    const own = randomUUID(), operator = randomUUID()
    await pool.query('INSERT INTO auth.sso_providers(id) VALUES ($1), ($2)', [own, operator])
    await pool.query(`INSERT INTO auth.sso_domains(id, sso_provider_id, domain) VALUES ($1, $2, 'grants.example'), ($3, $4, 'operator.example')`, [randomUUID(), own, randomUUID(), operator])
    const domains = await person('domain_admin', org.id)
    await one(domains, `SELECT gregale_auth.link_org_sso_provider($1, $2, 'member')`, [org.id, own], true)
    await pool.query(`SELECT gregale_auth.link_sso_organization($1, $2, 'member')`, [operator, org.id])
    const rules = async (idp, domain) => (await pool.query('SELECT self_service, in_domain FROM gregale_auth.sso_signup_rules($1, $2)', [idp, domain])).rows[0]
    assert.deepEqual(await rules(own, 'GRANTS.example'), { self_service: true, in_domain: true })
    assert.deepEqual(await rules(own, 'victim.example'), { self_service: true, in_domain: false })
    assert.deepEqual(await rules(operator, 'anything.example'), { self_service: false, in_domain: false })
    assert.deepEqual(await rules(randomUUID(), 'x.example'), { self_service: false, in_domain: false }, 'an unlinked provider is the operator\'s')
    // Changing the operator's provider through the organization makes it the organization's.
    await one(domains, 'SELECT gregale_auth.claim_org_sso_provider($1, $2)', [org.id, operator], true)
    assert.equal((await rules(operator, 'operator.example')).self_service, true)
    // A member without sso:manage cannot link or claim.
    await assert.rejects(one(admin, `SELECT gregale_auth.link_org_sso_provider($1, $2, 'member')`, [org.id, randomUUID()]), { message: 'forbidden' })
  })
})
