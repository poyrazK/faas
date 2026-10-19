import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { jwtVerify } from 'jose'
import { runtimeConfig, limits } from '../config.mjs'
import { createServer } from '../server.mjs'
import { main } from '../sql-deadline.mjs'

const env = { DATABASE_URL: 'postgres://restricted:private-password@localhost/db', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes' }

test('SQL deadline opt-in is strict and composes the single engine hook without adding pool capacity', () => {
  for (const value of ['', '0', 'true', '2', ' 1', 1, null]) assert.throws(() => runtimeConfig({ ...env, DATA_API_SQL_DEADLINE: value }), /SQL deadline/)
  for (const coordinated of [false, true]) {
    const base = { ...env, ...(coordinated ? { DATA_API_COORDINATED_REFRESH: '1' } : {}) }
    const legacy = runtimeConfig(base), config = runtimeConfig({ ...base, DATA_API_SQL_DEADLINE: '1' })
    assert.equal(legacy.sqlDeadline, false); assert.equal(config.sqlDeadline, true)
    assert.equal(legacy.postgrestEnv.PGRST_DB_PRE_REQUEST, coordinated ? 'gregale_data_api.check_request' : undefined)
    assert.equal(config.postgrestEnv.PGRST_DB_PRE_REQUEST, 'gregale_data_api.check_deadline_request')
    assert.equal(config.postgrestEnv.PGRST_DB_CONFIG, 'false', 'in-database engine settings cannot replace the opted-in hook')
    assert.equal(legacy.postgrestEnv.PGRST_DB_CONFIG, undefined)
    assert.equal(config.postgrestEnv.PGRST_DB_POOL, legacy.postgrestEnv.PGRST_DB_POOL)
  }
})

test('gateway overwrites reserved claims with one admission deadline, including refresh, and strips them when disabled', async t => {
  for (const mode of ['off', 'on', 'refresh']) {
    await t.test(mode, async t => {
      let now = 1700000000000, entered, release
      const pending = new Promise(resolve => { release = resolve })
      const verification = new Promise(resolve => { entered = resolve })
      t.mock.method(Date, 'now', () => now)
      const config = runtimeConfig({ ...env, ...(mode !== 'off' ? { DATA_API_SQL_DEADLINE: '1' } : {}), ...(mode === 'refresh' ? { DATA_API_COORDINATED_REFRESH: '1' } : {}) })
      config.fingerprint = 'a'.repeat(64)
      if (mode === 'refresh') config.refreshStatus = async () => ({ paused: false, fingerprint: config.fingerprint })
      let forwarded
      const upstream = http.createServer(async (req, res) => {
        req.resume(); forwarded = req.headers
        const { payload } = await jwtVerify(req.headers.authorization.slice(7), new TextEncoder().encode(config.secret.toString('base64url')), { algorithms: ['HS256'], currentDate: new Date(now) })
        res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify(payload))
      })
      await new Promise(resolve => upstream.listen(0, '127.0.0.1', resolve))
      const supplied = Object.freeze({ sub: 'private-subject', exp: 9999999999, role: 'owner', gregale_sql_deadline: Object.freeze({ version: 100, expires_at_ms: Number.MAX_SAFE_INTEGER, coordinated_refresh: false }) })
      const gateway = createServer(config, async () => { entered(); await pending; return supplied }, upstream.address().port, 3001, () => {})
      await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
      t.after(async () => {
        for (const server of [gateway, upstream]) { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)) }
      })
      const response = fetch(`http://127.0.0.1:${gateway.address().port}/rest/v1/notes`, { headers: { Authorization: 'Bearer private-token', 'X-Gregale-SQL-Deadline': String(Number.MAX_SAFE_INTEGER) } })
      await verification
      now += 2000; release()
      const claims = await (await response).json()
      assert.equal(claims.sub, supplied.sub); assert.equal(claims.role, 'restricted')
      assert.equal(forwarded['x-gregale-sql-deadline'], undefined)
      if (mode === 'off') assert.equal(claims.gregale_sql_deadline, undefined)
      else assert.deepEqual(claims.gregale_sql_deadline, { version: 1, expires_at_ms: 1700000000000 + limits.queryMs, coordinated_refresh: mode === 'refresh' })
      assert.equal(supplied.gregale_sql_deadline.version, 100, 'the verified payload must not be mutated')
    })
  }
})

test('owner command rejects invalid actions and mode settings before connecting', async () => {
  for (const args of [[], ['check'], ['check','role','extra'], ['delete','role']]) await assert.rejects(main({}, args), /Use sql-deadline/)
  await assert.rejects(main({ MIGRATION_DATABASE_URL: env.DATABASE_URL, DATA_API_COORDINATED_REFRESH: 'true' }, ['check','role']), /coordinated refresh/)
})
