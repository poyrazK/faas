import test from 'node:test'
import assert from 'node:assert/strict'
import { connectionObservationConfig, runtimeConfig, limits } from '../config.mjs'
import { createConnectionReporter, qualifiedConnectionEngine } from '../connection-observations.mjs'

const policy = { api_url: 'https://control.example', binding_id: '11111111-1111-4111-8111-111111111111', database_identity: 'a'.repeat(64), database_generation: 3, credential_generation: 2 }
const env = { DATA_API_CONNECTION_OBSERVATION: JSON.stringify(policy), FAAS_WORKLOAD_IDENTITY_ENDPOINT: 'http://127.0.0.1:2773/oidc/token' }

test('reports require explicit valid owner configuration and loopback workload identity', () => {
  assert.equal(connectionObservationConfig({}), undefined)
  const valid = connectionObservationConfig(env)
  assert.equal(valid.endpoint, 'https://control.example/v1/runtime/data-api/connection-observations')
  assert.equal(new URL(valid.identity).searchParams.get('audience'), 'gregale:data-api:connections')
  for (const bad of [{ api_url: 'http://control.example' }, { api_url: 'https://user:private@control.example' }, { api_url: 'https://control.example/?private' }, { binding_id: 'not-an-id' }, { credential_generation: 0 }, { database_generation: 1.5 }, { account_id: 'untrusted' }]) {
    assert.throws(() => connectionObservationConfig({ ...env, DATA_API_CONNECTION_OBSERVATION: JSON.stringify({ ...policy, ...bad }) }), /Invalid Data API/)
  }
  for (const endpoint of ['https://remote.example/oidc/token', 'http://169.254.169.254/oidc/token', 'http://127.0.0.1:2773/oidc/token?audience=other', undefined]) {
    assert.throws(() => connectionObservationConfig({ ...env, FAAS_WORKLOAD_IDENTITY_ENDPOINT: endpoint }), /Invalid Data API/)
  }
  assert.equal(qualifiedConnectionEngine('PostgREST 14.3\n'), true)
  assert.equal(qualifiedConnectionEngine('PostgREST 14.30\n'), false)
  assert.equal(qualifiedConnectionEngine('PostgREST 14.2\n'), false)
})

test('runtime reports fresh identity, actual engine pool and refresh reservation without credentials', async () => {
  for (const coordinated of [false, true]) {
    const config = runtimeConfig({ ...env, DATABASE_URL: 'postgres://private-role:private-password@localhost/db', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes', ...(coordinated ? { DATA_API_COORDINATED_REFRESH: '1' } : {}) })
    const requests = []; let identities = 0
    const fetchImpl = async (url, options) => {
      requests.push({ url, options })
      if (url === config.connectionObservation.identity) return Response.json({ token_type: 'Bearer', access_token: `private-token-${++identities}` })
      return new Response(null, { status: 204 })
    }
    const report = createConnectionReporter(config, async () => ({ state: 'observed', capacity: coordinated ? 1 : 2 }), fetchImpl)
    await report(); await report()
    assert.equal(identities, 2, 'identity must be refreshed after a restored snapshot')
    const sent = JSON.parse(requests[1].options.body)
    assert.equal(sent.engine_connections, coordinated ? 1 : 2)
    assert.equal(sent.refresh_connections, coordinated ? 1 : 0)
    assert.equal(sent.state, 'observed')
    assert.equal(sent.credential_generation, 2)
    assert.equal(sent.database_generation, 3)
    assert.equal(sent.role, 'private-role')
    assert.equal(requests[3].options.headers.Authorization, 'Bearer private-token-2')
    assert.equal(requests[1].options.redirect, 'error')
    assert.equal(requests[0].options.headers, undefined)
    assert.doesNotMatch(requests[1].options.body, /private-password|postgres:\/\/|private-token/)
    assert.ok(Date.now() - Date.parse(sent.observed_at) < 1000)
  }
})

test('failed or contradictory pool observations invalidate earlier reports and do not invent zero usage', async () => {
  const config = { connectionObservation: connectionObservationConfig(env), role: 'restricted' }
  for (const counts of [{ state: 'unknown' }, { state: 'observed', capacity: 999 }]) {
    let sent
    await createConnectionReporter(config, async () => counts, async (url, options) => {
      if (url === config.connectionObservation.identity) return Response.json({ token_type: 'Bearer', access_token: 'private-token' })
      sent = JSON.parse(options.body)
      return new Response(null, { status: 204 })
    })()
    assert.equal(sent.state, 'unknown')
    assert.equal(sent.engine_connections, limits.pool)
    assert.equal(sent.refresh_connections, 0)
  }
})

test('identity failures, oversized responses, redirects and shutdown never publish evidence', async () => {
  const config = { connectionObservation: connectionObservationConfig(env), role: 'restricted' }
  for (const response of [new Response('private-error', { status: 500 }), Response.json({ token_type: 'Bearer', access_token: 'x'.repeat(limits.healthBytes + 1) }), Response.json({ token_type: 'other', access_token: 'token' })]) {
    let calls = 0
    await createConnectionReporter(config, async () => ({ state: 'observed', capacity: 2 }), async () => { calls++; return response })()
    assert.equal(calls, 1)
  }
  let calls = 0
  const controller = new AbortController(); controller.abort()
  await createConnectionReporter(config, async () => ({ state: 'observed', capacity: 2 }), async () => { calls++; throw Error('private-error') }, controller.signal)()
  assert.equal(calls, 0)
  await createConnectionReporter({}, () => { throw Error('disabled reporter touched SQL') })()
})
