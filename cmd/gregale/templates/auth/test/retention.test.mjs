import test from 'node:test'
import assert from 'node:assert/strict'
import { createAnonymousPruner, createAuditPruner, createOrgAuditPruner } from '../retention.mjs'
import { limits, runtimeConfig } from '../config.mjs'

function fakePool(remaining) {
  const calls = []
  return { calls, query: async (sql, args) => {
    calls.push({ sql, args })
    const n = Math.min(remaining, args[1])
    remaining -= n
    return { rowCount: n }
  } }
}

test('prunes old activity in batches, at most once per interval', async () => {
  let clock = 0
  const logs = []
  const pool = fakePool(limits.auditPruneBatch * 2 + 7)
  const pruner = createAuditPruner({ pool, days: 90, now: () => clock, log: line => logs.push(JSON.parse(line)) })
  await pruner.poke()
  assert.equal(pool.calls.length, 3)
  assert.match(pool.calls[0].sql, /DELETE FROM auth\.audit_log_entries WHERE id IN/)
  assert.deepEqual(pool.calls[0].args, [90, limits.auditPruneBatch])
  assert.deepEqual(logs, [{ level: 'info', event: 'audit_log_pruned', count: limits.auditPruneBatch * 2 + 7 }])
  await pruner.poke()
  assert.equal(pool.calls.length, 3, 'not again within the interval')
  clock += limits.auditPruneIntervalSeconds * 1000
  await pruner.poke()
  assert.equal(pool.calls.length, 4)
})

test('the organization audit log follows the same retention', async () => {
  const pool = fakePool(3)
  await createOrgAuditPruner({ pool, days: 90, log: () => {} }).poke()
  assert.match(pool.calls[0].sql, /DELETE FROM gregale_auth\.org_audit_log WHERE id IN/)
  assert.deepEqual(pool.calls[0].args, [90, limits.auditPruneBatch])
  const idle = fakePool(0)
  await createOrgAuditPruner({ pool: idle, days: null }).poke()
  assert.equal(idle.calls.length, 0, 'off while activity is kept forever')
})

test('a batch cap bounds one run, and no retention means no pruning', async () => {
  const pool = fakePool(Infinity)
  await createAuditPruner({ pool, days: 30, log: () => {} }).poke()
  assert.equal(pool.calls.length, limits.auditPruneBatches)
  const idle = fakePool(10)
  await createAuditPruner({ pool: idle, days: null }).poke()
  assert.equal(idle.calls.length, 0)
})

test('retention days are validated and reported', () => {
  const env = { DATABASE_URL: 'postgresql://u:p@db.example.com/app', AUTH_EXTERNAL_URL: 'https://shop-auth.gregale.app', AUTH_SITE_URL: 'https://shop.example.com', AUTH_JWT_SECRET: 'x'.repeat(48),
    AUTH_JWT_KEY_0A1B2C3D: JSON.stringify({ kty: 'EC', crv: 'P-256', alg: 'ES256', kid: 'gregale-0a1b2c3d', x: 'f83OJ3D2xF1Bg8vub9tLe1gHMzV76e8Tus9uPHvRVEU', y: 'x_FEzRu9m36HLN_tue659LNpXW6pCyStikYjKIWI5a0', d: 'jpsQnnGQmL-YBIffH1136cspYG6-0iY7X1fCE9-E9LI' }), AUTH_JWT_SIGNER_0A1B2C3D: '1700000000' }
  assert.equal(runtimeConfig({ ...env, AUTH_AUDIT_RETENTION_DAYS: '90' }).settings.activity.retention_days, 90)
  for (const bad of ['0', '-1', '3651', '90d', '1.5']) assert.throws(() => runtimeConfig({ ...env, AUTH_AUDIT_RETENTION_DAYS: bad }), /AUTH_AUDIT_RETENTION_DAYS/, bad)
  const anonymous = runtimeConfig({ ...env, GOTRUE_EXTERNAL_ANONYMOUS_USERS_ENABLED: 'true', AUTH_ANONYMOUS_RETENTION_DAYS: '30', AUTH_IDENTITY_LINKING: '1' })
  assert.deepEqual(anonymous.settings.anonymous, { enabled: true, retention_days: 30 })
  assert.equal(anonymous.settings.identity_linking, true)
  assert.equal(anonymous.gotrueEnv.GOTRUE_SECURITY_MANUAL_LINKING_ENABLED, 'true')
  assert.equal(runtimeConfig(env).gotrueEnv.GOTRUE_SECURITY_MANUAL_LINKING_ENABLED, 'false')
  for (const bad of ['0', '3651', '30d']) assert.throws(() => runtimeConfig({ ...env, AUTH_ANONYMOUS_RETENTION_DAYS: bad }), /AUTH_ANONYMOUS_RETENTION_DAYS/, bad)
  assert.throws(() => runtimeConfig({ ...env, AUTH_IDENTITY_LINKING: 'yes' }), /AUTH_IDENTITY_LINKING/)
  assert.equal(runtimeConfig({ ...env, AUTH_SELF_DELETION: '1' }).settings.self_deletion, true)
  assert.equal(runtimeConfig(env).settings.self_deletion, false)
  assert.throws(() => runtimeConfig({ ...env, AUTH_SELF_DELETION: 'on' }), /AUTH_SELF_DELETION/)
  assert.equal(runtimeConfig({ ...env, AUTH_ORG_CLAIMS: '1' }).settings.organization_claims, true)
  assert.equal(runtimeConfig(env).orgClaims, false)
  assert.throws(() => runtimeConfig({ ...env, AUTH_ORG_CLAIMS: 'on' }), /AUTH_ORG_CLAIMS/)
  assert.throws(() => runtimeConfig({ ...env, GOTRUE_SECURITY_MANUAL_LINKING_ENABLED: 'true' }), /managed by Gregale/, 'linking is a typed setting, not a raw secret')
})

test('deletes anonymous users idle longer than their retention', async () => {
  const logs = []
  const pool = fakePool(3)
  await createAnonymousPruner({ pool, days: 30, log: line => logs.push(JSON.parse(line)) }).poke()
  assert.match(pool.calls[0].sql, /DELETE FROM auth\.users WHERE id IN/)
  assert.match(pool.calls[0].sql, /u\.is_anonymous AND u\.created_at < now\(\) - make_interval\(days => \$1\)/)
  assert.match(pool.calls[0].sql, /NOT EXISTS \(SELECT 1 FROM auth\.sessions/)
  assert.deepEqual(pool.calls[0].args, [30, limits.auditPruneBatch])
  assert.deepEqual(logs, [{ level: 'info', event: 'anonymous_users_pruned', count: 3 }])
  const kept = fakePool(3)
  await createAnonymousPruner({ pool: kept, days: null }).poke()
  assert.equal(kept.calls.length, 0, 'no retention keeps anonymous users')
})
