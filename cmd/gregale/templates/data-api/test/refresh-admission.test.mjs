import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { decodeJwt } from 'jose'
import { createServer } from '../server.mjs'
import { runtimeConfig } from '../config.mjs'
import { refreshTokenHash } from '../refresh.mjs'

async function fixture(t) {
  const owner = 'a'.repeat(64), fingerprint = 'b'.repeat(64)
  const config = runtimeConfig({ DATABASE_URL: 'postgres://restricted:p@localhost/db', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes', DATA_API_COORDINATED_REFRESH: '1' })
  config.fingerprint = fingerprint
  const state = { paused: true, status: 200, reads: 0, calls: [], records: [] }
  config.refreshStatus = async token => { state.reads++; return { paused: state.paused, fingerprint, owns_pause: token === owner } }
  const upstream = http.createServer((req, res) => {
    state.calls.push({ path: req.url, claims: decodeJwt(req.headers.authorization.slice(7)) })
    req.resume(); res.writeHead(state.status, { 'Content-Type': 'application/json' }); res.end('[]')
  })
  await new Promise(resolve => upstream.listen(0, '127.0.0.1', resolve))
  const verify = async token => {
    if (token !== 'valid') throw Error('invalid')
    return { sub: 'alice', exp: 9999999999, gregale_refresh_hash: refreshTokenHash(owner), gregale_serving_fingerprint: 'c'.repeat(64) }
  }
  const gateway = createServer(config, verify, upstream.address().port, undefined, record => state.records.push(record))
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  t.after(async () => {
    gateway.closeAllConnections(); upstream.closeAllConnections()
    await Promise.all([new Promise(resolve => gateway.close(resolve)), new Promise(resolve => upstream.close(resolve))])
  })
  const url = `http://127.0.0.1:${gateway.address().port}/rest/v1/notes`
  const request = async (token, refresh) => {
    const response = await fetch(url, { headers: { ...(token ? { Authorization: `Bearer ${token}` } : {}), ...(refresh ? { 'X-Gregale-Refresh-Token': refresh } : {}) } })
    const body = await response.text()
    return { status: response.status, body }
  }
  return { config, state, request, owner, fingerprint }
}

test('paused traffic is rejected before the engine, while authentication and owner verification remain strict', async t => {
  const { state, request, owner, fingerprint } = await fixture(t)
  assert.equal((await request()).status, 401)
  assert.equal((await request('invalid')).status, 401)
  assert.equal(state.reads, 0)
  for (const token of [undefined, 'd'.repeat(64)]) {
    const result = await request('valid', token)
    assert.equal(result.status, 503)
    assert.equal(JSON.parse(result.body).code, 'data_api_refresh_paused')
  }
  assert.equal(state.calls.length, 0, 'pause denials must not trigger engine recovery')
  assert.ok(state.records.some(record => record.code === 'data_api_refresh_paused'))
  assert.equal(JSON.stringify(state.records).includes(owner), false)
  assert.equal((await request('valid', owner)).status, 200)
  assert.equal(state.calls[0].claims.gregale_refresh_hash, refreshTokenHash(owner))
  assert.equal(state.calls[0].claims.gregale_serving_fingerprint, fingerprint)
})

test('missing or malformed refresh state fails closed before engine access', async t => {
  const { config, state, request, fingerprint } = await fixture(t)
  for (const reader of [undefined, async () => { throw Error('unavailable') }, async () => null, async () => ({ paused: 'false', fingerprint }), async () => ({ paused: false, fingerprint: 'invalid' })]) {
    config.refreshStatus = reader
    const result = await request('valid')
    assert.equal(result.status, 503)
    assert.equal(JSON.parse(result.body).code, 'data_api_refresh_unavailable')
  }
  assert.equal(state.records.at(-1).code, 'data_api_refresh_unavailable')
  assert.equal(state.records.at(-1).outcome, 'unavailable')
  config.refreshStatus = async () => ({ paused: true, fingerprint, owns_pause: 'true' })
  assert.equal((await request('valid')).status, 503)
  assert.equal(state.calls.length, 0)
})

test('the SQL fence still controls requests admitted before a concurrent pause', async t => {
  const { state, request } = await fixture(t)
  state.paused = false
  state.status = 503 // The database pre-request hook wins a pause after gateway admission.
  assert.equal((await request('valid')).status, 503)
  assert.equal(state.calls.length, 1)
  assert.equal(state.calls[0].claims.gregale_refresh_hash, undefined, 'forged JWT coordination claim must be removed')
})
