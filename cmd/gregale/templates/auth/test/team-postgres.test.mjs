// ADR-1710: the membership change and its retry/audit receipt commit together.
import test from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID, createHash } from 'node:crypto'
import pg from 'pg'
import { emailConsent, resolveEmailRecipient } from '../product-email.mjs'
import { routeTeam } from '../team.mjs'
import { applyGregaleMigrations } from '../migrate.mjs'

const database = process.env.GREGALE_AUTH_TEAM_TEST_DATABASE_URL
test('reviewed team actions against PostgreSQL', { skip: !database }, async t => {
  assert.match(new URL(database).pathname, /^\/product_control_team_auth_[a-z0-9_]+$/, 'a dedicated disposable test database is required')
  const pool = new pg.Pool({ connectionString: database, max: 8 })
  t.after(() => pool.end())
  await pool.query(`DROP SCHEMA IF EXISTS gregale_auth CASCADE; DROP SCHEMA IF EXISTS auth CASCADE;
    CREATE SCHEMA auth; CREATE SCHEMA gregale_auth;
    CREATE TABLE auth.users(id uuid PRIMARY KEY,email text,email_confirmed_at timestamptz,deleted_at timestamptz,banned_until timestamptz,is_anonymous boolean NOT NULL DEFAULT false,is_sso_user boolean NOT NULL DEFAULT false,role varchar(255));
    CREATE TABLE auth.sessions(id uuid PRIMARY KEY,user_id uuid REFERENCES auth.users(id),not_after timestamptz);
    CREATE TABLE auth.audit_log_entries(id uuid PRIMARY KEY,created_at timestamptz,payload jsonb);
    CREATE TABLE auth.sso_providers(id uuid PRIMARY KEY);
    CREATE TABLE auth.identities(id uuid PRIMARY KEY,user_id uuid REFERENCES auth.users(id),provider text);
    CREATE TABLE auth.sso_domains(id uuid PRIMARY KEY,sso_provider_id uuid REFERENCES auth.sso_providers(id),domain text);`)
  // Users from before GOTRUE_JWT_DEFAULT_GROUP_NAME have an empty role (sql/0012).
  const roles = { [randomUUID()]: ['', 'authenticated'], [randomUUID()]: [null, 'authenticated'], [randomUUID()]: ['support', 'support'] }
  for (const [id, [before]] of Object.entries(roles)) await pool.query('INSERT INTO auth.users(id,email,role) VALUES($1,$2,$3)', [id, `${id}@example.test`, before])
  const client = await pool.connect()
  try {
    for (let i = 0; i < 2; i++) await applyGregaleMigrations(client, undefined, JSON.stringify({ roles: { operator: ['org:read'], viewer: ['org:read'], manager: ['org:read', 'members:manage'] } }))
  } finally { client.release() }
  assert.equal((await pool.query('SELECT count(*)::int AS count FROM gregale_auth.schema_versions')).rows[0].count, 12)
  for (const [id, [, after]] of Object.entries(roles)) assert.equal((await pool.query('SELECT role FROM auth.users WHERE id=$1', [id])).rows[0].role, after)

  async function user(email = `${randomUUID()}@example.test`, confirmed = true) {
    const value = { id: randomUUID(), session: randomUUID(), email }
    await pool.query('INSERT INTO auth.users(id,email,email_confirmed_at) VALUES($1,$2,CASE WHEN $3 THEN now() ELSE NULL END)', [value.id, email, confirmed])
    await pool.query('INSERT INTO auth.sessions(id,user_id) VALUES($1,$2)', [value.session, value.id])
    return value
  }
  function run(actor, intercept) {
    return async work => {
      const connection = await pool.connect()
      try {
        await connection.query('BEGIN')
        await connection.query(`SELECT set_config('request.jwt.claims',$1,true)`, [JSON.stringify({ sub: actor.id, session_id: actor.session })])
        const db = intercept ? { query: async (...args) => { await intercept(args[0]); return connection.query(...args) } } : connection
        const value = await work(db)
        await connection.query('COMMIT'); return value
      } catch (error) { await connection.query('ROLLBACK'); throw error }
      finally { connection.release() }
    }
  }
  async function fixture(twoOwners = false) {
    const owner = await user(), member = await user()
    const org = (await run(owner)(c => c.query('SELECT id FROM gregale_auth.create_organization($1,$2)', [`org-${randomUUID()}`, 'Test workspace']))).rows[0].id
    await pool.query('INSERT INTO gregale_auth.memberships(org_id,user_id,role) VALUES($1,$2,$3)', [org, member.id, twoOwners ? 'owner' : 'member'])
    return { owner, member, org }
  }
  const get = f => routeTeam({ run: run(f.owner), method: 'GET', parts: ['orgs', f.org, 'team'] }).then(([, value]) => value)
  const post = (f, body, actor = f.owner, invitations, intercept) => routeTeam({ run: run(actor, intercept), method: 'POST', parts: ['orgs', f.org, 'team-actions'], body, invitations }).then(([, value]) => value)
  const history = (f, cursor = '') => routeTeam({ run: run(f.owner), method: 'GET', parts: ['orgs', f.org, 'team-actions'], query: new URLSearchParams({ cursor }) }).then(([, value]) => value)
  const roleOf = async f => (await pool.query('SELECT role FROM gregale_auth.memberships WHERE org_id=$1 AND user_id=$2', [f.org, f.member.id])).rows[0]?.role
  const countActions = async f => (await pool.query('SELECT count(*)::int AS count FROM gregale_auth.team_actions WHERE org_id=$1', [f.org])).rows[0].count
  async function request(f, action, fields = {}) { return { id: randomUUID(), action, ...fields, expected_snapshot: (await get(f)).snapshot } }

  await t.test('concurrent retries share one mutation and old retries cannot restore a role', async () => {
    const f = await fixture(), body = await request(f, 'set_role', { target_id: f.member.id, role: 'operator' })
    const replies = await Promise.all(Array.from({ length: 8 }, () => post(f, body)))
    for (const reply of replies) assert.deepEqual(reply.receipt, replies[0].receipt)
    assert.equal(replies.filter(reply => !reply.replayed).length, 1)
    assert.equal(await countActions(f), 1); assert.equal(await roleOf(f), 'operator')
    await run(f.owner)(c => c.query('SELECT gregale_auth.set_member_role($1,$2,$3)', [f.org, f.member.id, 'viewer']))
    assert.equal((await post(f, body)).replayed, true); assert.equal(await roleOf(f), 'viewer')
    await assert.rejects(post(f, { ...body, role: 'owner' }), error => error.code === 'team_action_conflict')
    assert.equal(await roleOf(f), 'viewer')
  })
  await t.test('stale membership and role-definition snapshots apply nothing', async () => {
    const f = await fixture(), before = await request(f, 'set_role', { target_id: f.member.id, role: 'operator' })
    await run(f.owner)(c => c.query('SELECT gregale_auth.set_member_role($1,$2,$3)', [f.org, f.member.id, 'viewer']))
    await assert.rejects(post(f, before), error => error.code === 'team_review_changed')
    const changed = await request(f, 'set_role', { target_id: f.member.id, role: 'operator' })
    await pool.query(`UPDATE gregale_auth.roles SET permissions=ARRAY['org:read','doc:write'] WHERE name='operator'`)
    await assert.rejects(post(f, changed), error => error.code === 'team_review_changed')
    assert.equal(await countActions(f), 0); assert.equal(await roleOf(f), 'viewer')
  })
  await t.test('role configuration waits until a reviewed action commits', async () => {
    const f = await fixture(), body = await request(f, 'set_role', { target_id: f.member.id, role: 'operator' })
    let release, reached, finished = false
    const gate = new Promise(resolve => { release = resolve }), writing = new Promise(resolve => { reached = resolve })
    const changing = post(f, body, f.owner, undefined, async sql => { if (sql.includes('team_record_action')) { reached(); await gate } })
    await writing
    const configuring = pool.query(`UPDATE gregale_auth.roles SET permissions=ARRAY['org:read'] WHERE name='operator'`).then(() => { finished = true })
    try { await new Promise(resolve => setTimeout(resolve, 30)); assert.equal(finished, false) }
    finally { release() }
    await changing; await configuring; assert.equal(await roleOf(f), 'operator')
  })
  await t.test('foreign receipt IDs roll back a membership mutation', async () => {
    const first = await fixture(), second = await fixture()
    const used = await request(first, 'set_role', { target_id: first.member.id, role: 'operator' }); await post(first, used)
    const collision = await request(second, 'set_role', { target_id: second.member.id, role: 'operator' }); collision.id = used.id
    await assert.rejects(post(second, collision), error => error.code === '23505')
    assert.equal(await roleOf(second), 'member'); assert.equal(await countActions(second), 0)
    const [, missing] = await run(second.owner)(async c => [null, (await c.query('SELECT gregale_auth.team_read_action($1,$2) AS action', [second.org, used.id])).rows[0].action])
    assert.equal(missing, null)
  })
  await t.test('authority is rechecked after waiting for role configuration', async () => {
    const f = await fixture()
    await run(f.owner)(c => c.query('SELECT gregale_auth.set_member_role($1,$2,$3)', [f.org, f.member.id, 'manager']))
    const configuration = await pool.connect()
    await configuration.query('BEGIN')
    await configuration.query(`UPDATE gregale_auth.roles SET permissions=ARRAY['members:manage'] WHERE name='manager'`)
    // Attach rejection handling before releasing the blocking transaction.
    const denied = assert.rejects(get({ ...f, owner: f.member }), error => error.code === '42501')
    try {
      let waiting = false
      for (let i = 0; i < 100 && !waiting; i++) {
        waiting = (await pool.query(`SELECT EXISTS(SELECT 1 FROM pg_locks WHERE relation='gregale_auth.roles'::regclass AND mode='ShareLock' AND NOT granted) AS waiting`)).rows[0].waiting
        if (!waiting) await new Promise(resolve => setTimeout(resolve, 5))
      }
      assert.equal(waiting, true, 'team state must wait for the role-definition lock')
    } finally {
      await configuration.query('COMMIT'); configuration.release()
    }
    try { await denied }
    finally { await pool.query(`UPDATE gregale_auth.roles SET permissions=ARRAY['org:read','members:manage'] WHERE name='manager'`) }
  })
  await t.test('last-owner protection and competing owner changes remain transactional', async () => {
    const single = await fixture(), body = await request(single, 'remove_member', { target_id: single.owner.id })
    await assert.rejects(post(single, body), error => error.code === '23514' && error.message === 'last_owner')
    assert.equal(await countActions(single), 0)
    const f = await fixture(true), a = await request(f, 'set_role', { target_id: f.member.id, role: 'member' })
    const [, bState] = await routeTeam({ run: run(f.member), method: 'GET', parts: ['orgs', f.org, 'team'] })
    const b = { id: randomUUID(), action: 'set_role', target_id: f.owner.id, role: 'member', expected_snapshot: bState.snapshot }
    const outcomes = await Promise.allSettled([post(f, a), post(f, b, f.member)])
    assert.equal(outcomes.filter(value => value.status === 'fulfilled').length, 1)
    assert.equal((await pool.query(`SELECT count(*)::int AS count FROM gregale_auth.memberships WHERE org_id=$1 AND role='owner'`, [f.org])).rows[0].count, 1)
  })
  await t.test('manual invitations are single-use, email-bound and token-free in audit history', async () => {
    const f = await fixture(), email = `${randomUUID()}@example.test`
    const body = await request(f, 'invite', { email, role: 'member', expires_in: 604800 })
    const created = await post(f, body); assert.match(created.token, /^[A-Za-z0-9_-]{43}$/)
    const replay = await post(f, body); assert.equal(replay.token, undefined); assert.equal(replay.replayed, true)
    const digest = createHash('sha256').update(Buffer.from(created.token, 'base64url')).digest()
    const row = (await pool.query('SELECT token_hash FROM gregale_auth.invitations WHERE id=$1', [created.receipt.result.id])).rows[0]
    assert.deepEqual(row.token_hash, digest)
    const all = (await pool.query('SELECT to_jsonb(a)::text AS data FROM gregale_auth.team_actions a WHERE id=$1', [body.id])).rows[0].data
    assert.equal(all.includes(created.token), false)
    const wrong = await user(), unconfirmed = await user(email, false)
    await assert.rejects(run(wrong)(c => c.query('SELECT gregale_auth.accept_invitation($1)', [digest])), error => error.code === '42501')
    await assert.rejects(run(unconfirmed)(c => c.query('SELECT gregale_auth.accept_invitation($1)', [digest])), error => error.code === '42501')
    await pool.query('UPDATE auth.users SET email_confirmed_at=now() WHERE id=$1', [unconfirmed.id])
    const accepted = await run(unconfirmed)(c => c.query('SELECT org_id,user_id,role FROM gregale_auth.accept_invitation($1)', [digest]))
    assert.equal(accepted.rows[0].org_id, f.org)
    await assert.rejects(run(unconfirmed)(c => c.query('SELECT gregale_auth.accept_invitation($1)', [digest])), error => error.code === 'P0002')
    assert.equal((await history(f)).actions.length, 1)
  })
  await t.test('revoked and expired invitations cannot be accepted', async () => {
    for (const kind of ['revoked', 'expired']) {
      const f = await fixture(), invited = await user()
      const created = await post(f, await request(f, 'invite', { email: invited.email, role: 'member' }))
      if (kind === 'revoked') {
        const revoke = await request(f, 'revoke_invitation', { target_id: created.receipt.result.id })
        await post(f, revoke); assert.equal((await post(f, revoke)).replayed, true)
      } else {
        await pool.query(`UPDATE gregale_auth.invitations SET expires_at=now()-interval '1 second' WHERE id=$1`, [created.receipt.result.id])
        await assert.rejects(post(f, await request(f, 'revoke_invitation', { target_id: created.receipt.result.id })), error => error.status === 404)
        assert.equal(await countActions(f), 1)
      }
      const digest = createHash('sha256').update(Buffer.from(created.token, 'base64url')).digest()
      await assert.rejects(run(invited)(c => c.query('SELECT gregale_auth.accept_invitation($1)', [digest])), error => error.code === 'P0002')
    }
  })
  await t.test('email delivery is not repeated and a failed send rolls back its invitation', async () => {
    const f = await fixture(); let sent = 0
    const delivery = { external: 'https://auth.example.test', app: 'Product', mailer: { send: async message => { sent++; assert.match(message.text, /ui\/invitation/); assert.ok(!JSON.stringify(message).includes('Bearer')) } } }
    const body = await request(f, 'invite', { email: `${randomUUID()}@example.test`, role: 'member' })
    const created = await post(f, body, f.owner, delivery); assert.equal(created.token, undefined); assert.equal(created.receipt.result.delivered, true)
    await post(f, body, f.owner, delivery); assert.equal(sent, 1)
    const failed = await request(f, 'invite', { email: `${randomUUID()}@example.test`, role: 'member' })
    await assert.rejects(post(f, failed, f.owner, { ...delivery, mailer: { send: async () => { throw new Error('fixture send failed') } } }))
    assert.equal((await pool.query('SELECT count(*)::int AS count FROM gregale_auth.invitations WHERE org_id=$1', [f.org])).rows[0].count, 1)
    assert.equal(await countActions(f), 1)
  })
  await t.test('members without workspace management authority cannot use the new boundary', async () => {
    const f = await fixture(), body = await request(f, 'set_role', { target_id: f.member.id, role: 'operator' })
    await assert.rejects(post(f, body, f.member), error => error.code === '42501')
    await pool.query('DELETE FROM auth.sessions WHERE id=$1', [f.owner.session])
    await assert.rejects(post(f, body), error => error.code === '28000')
    assert.equal(await roleOf(f), 'member'); assert.equal(await countActions(f), 0)
  })
  await t.test('history pagination is scoped and contains no repeated actions', async () => {
    const f = await fixture()
    for (let i = 0; i < 101; i++) await post(f, await request(f, 'set_role', { target_id: f.member.id, role: i % 2 ? 'member' : 'operator' }))
    const first = await history(f), second = await history(f, first.next_cursor)
    assert.equal(first.actions.length, 100); assert.equal(second.actions.length, 1); assert.equal(second.next_cursor, undefined)
    assert.equal(new Set([...first.actions, ...second.actions].map(action => action.id)).size, 101)
    const other = await fixture()
    await assert.rejects(history(other, first.next_cursor), error => error.code === 'invalid_team_cursor')
    assert.ok(first.actions.every(action => !('fingerprint' in action)))
  })
  await t.test('personal notification grants follow live verified email and membership', async () => {
    const f = await fixture(), product = randomUUID()
    const parts = ['orgs', f.org, 'email-recipient'], path = `/orgs/${f.org}/email-recipient/${product}`
    const issue = async () => (await emailConsent(run(f.member), 'POST', parts, { product_id: product }))[1]
    let grant = await issue()
    const resolve = async () => (await resolveEmailRecipient(pool, 'GET', path, `Product-Recipient ${grant.token}`))[1]
    assert.equal((await resolve()).email, f.member.email)
    assert.equal((await resolve()).actor_id, f.member.id)
    await pool.query("UPDATE gregale_auth.memberships SET role='viewer' WHERE org_id=$1 AND user_id=$2", [f.org, f.member.id])
    assert.equal((await resolve()).role, 'viewer')
    await assert.rejects(resolveEmailRecipient(pool, 'GET', `/orgs/${f.org}/email-recipient/${randomUUID()}`, `Product-Recipient ${grant.token}`), e => e.status === 403)
    await pool.query("UPDATE auth.users SET email='changed@example.test' WHERE id=$1", [f.member.id])
    await assert.rejects(resolve(), e => e.status === 403)
    grant = await issue(); assert.equal((await resolve()).email, 'changed@example.test')
    for (const column of ['banned_until', 'deleted_at']) {
      await pool.query(`UPDATE auth.users SET ${column}=now()+interval '1 hour' WHERE id=$1`, [f.member.id])
      await assert.rejects(resolve(), e => e.status === 403)
      await pool.query(`UPDATE auth.users SET ${column}=NULL WHERE id=$1`, [f.member.id])
    }
    await pool.query('UPDATE auth.users SET email_confirmed_at=NULL WHERE id=$1', [f.member.id])
    await assert.rejects(resolve(), e => e.status === 403)
    await assert.rejects(issue(), e => e.code === '42501')
    await pool.query('UPDATE auth.users SET email_confirmed_at=now() WHERE id=$1', [f.member.id])
    await pool.query('DELETE FROM gregale_auth.memberships WHERE org_id=$1 AND user_id=$2', [f.org, f.member.id])
    await assert.rejects(resolve(), e => e.status === 403)
    await pool.query("INSERT INTO gregale_auth.memberships(org_id,user_id,role) VALUES($1,$2,'member')", [f.org, f.member.id])
    await assert.rejects(resolve(), e => e.status === 403)
    grant = await issue(); assert.equal((await resolve()).email, 'changed@example.test')
    await pool.query("UPDATE gregale_auth.product_email_recipients SET expires_at=now()-interval '1 second' WHERE user_id=$1", [f.member.id])
    await assert.rejects(resolve(), e => e.status === 403)
  })

})
