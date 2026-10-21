import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { once } from 'node:events'
import { generateKeyPairSync, randomUUID } from 'node:crypto'
import { signingKey } from '../admin.mjs'
import { route } from '../authz.mjs'
import { limits } from '../config.mjs'
import { createServer } from '../server.mjs'
import { adminRoute, createAuthenticator, createRateLimiter, issueToken, mintToken, newSecret, organizationRoute, secretHash, serviceAccountAllowed, tokenRequest, verificationKeys, verifyToken } from '../serviceaccounts.mjs'

function jwks(...ids) {
  return JSON.stringify(ids.map((kid, i) => {
    const jwk = generateKeyPairSync('ec', { namedCurve: 'P-256' }).privateKey.export({ format: 'jwk' })
    return { ...jwk, alg: 'ES256', kid, key_ops: i === 0 ? ['sign', 'verify'] : ['verify'] }
  }))
}

const issuer = 'https://shop-auth.example.test/auth/v1'
const form = 'application/x-www-form-urlencoded'
const basic = (id, secret) => `Basic ${Buffer.from(`${encodeURIComponent(id)}:${encodeURIComponent(secret)}`).toString('base64')}`
const payload = token => JSON.parse(Buffer.from(token.split('.')[1], 'base64url'))

test('a client-credentials request authenticates with Basic or in the body, never both', () => {
  const id = randomUUID()
  const { secret } = newSecret()
  assert.match(secret, /^gasa_[A-Za-z0-9_-]{43}$/)
  const viaBasic = tokenRequest(form, Buffer.from('grant_type=client_credentials&scope=docs:read+docs:read+billing:read'), basic(id.toUpperCase(), secret))
  assert.deepEqual({ ...viaBasic, challenge: undefined }, { clientId: id, secret, scope: ['billing:read', 'docs:read'], challenge: undefined })
  assert.equal(viaBasic.challenge['WWW-Authenticate'], 'Basic realm="service-accounts"')
  const viaBody = tokenRequest('application/json; charset=utf-8', Buffer.from(JSON.stringify({ grant_type: 'client_credentials', client_id: id, client_secret: secret })))
  assert.deepEqual(viaBody, { clientId: id, secret, scope: null, challenge: {} })
  // Every other grant is GoTrue's.
  assert.equal(tokenRequest(form, Buffer.from('grant_type=authorization_code&code=x'), undefined), null)
  assert.equal(tokenRequest('text/plain', Buffer.from('grant_type=client_credentials'), undefined), null)
  assert.equal(tokenRequest('application/json', Buffer.from('[1]'), undefined), null)
  const refused = (body, authorization, error, status) => assert.throws(() => tokenRequest(form, Buffer.from(body), authorization), err => err.error === error && err.status === status, `${body} ${authorization}`)
  refused(`grant_type=client_credentials&client_id=${id}`, basic(id, secret), 'invalid_request', 400)
  refused('grant_type=client_credentials', undefined, 'invalid_client', 401)
  refused('grant_type=client_credentials', `Bearer ${secret}`, 'invalid_client', 401)
  refused(`grant_type=client_credentials&client_id=not-a-uuid&client_secret=${secret}`, undefined, 'invalid_client', 401)
  refused('grant_type=client_credentials', basic(id, 'gasa_short'), 'invalid_client', 401)
  refused('grant_type=client_credentials&scope=', basic(id, secret), 'invalid_scope', 400)
  refused('grant_type=client_credentials&scope=*', basic(id, secret), 'invalid_scope', 400)
  refused(`grant_type=client_credentials&scope=${Array.from({ length: 65 }, (_, i) => `p${i}`).join('+')}`, basic(id, secret), 'invalid_scope', 400)
})

test('tokens verify like a person\'s and carry the account, its secret and its organization', () => {
  const keys = jwks('gregale-0a1b2c3d', 'gregale-ffff0001')
  const signer = signingKey(keys)
  const account = { id: randomUUID(), secret_id: randomUUID(), org_id: randomUUID(), org_slug: 'acme', role: 'deployer', permissions: ['docs:read', 'docs:write'] }
  const now = 1_800_000_000_000
  const token = mintToken({ signer, issuer }, account, null, now)
  const claims = verifyToken(token, { keys: verificationKeys(keys), issuer }, now)
  assert.deepEqual({ ...claims, jti: undefined }, {
    iss: issuer, aud: 'authenticated', sub: account.id, role: 'authenticated', iat: now / 1000, exp: now / 1000 + limits.accessTokenSeconds, jti: undefined,
    service_account: true, secret_id: account.secret_id, org_id: account.org_id, org_slug: 'acme', org_role: 'deployer', org_permissions: ['docs:read', 'docs:write'],
  })
  const narrowed = payload(mintToken({ signer, issuer }, account, ['docs:read'], now))
  assert.equal(narrowed.scope, 'docs:read')
  assert.deepEqual(narrowed.org_permissions, ['docs:read'])
  const refused = (value, options, at = now) => assert.throws(() => verifyToken(value, options, at), { code: 'not_authenticated' })
  refused(token, { keys: verificationKeys(keys), issuer }, now + limits.accessTokenSeconds * 1000)
  refused(token, { keys: verificationKeys(keys), issuer: 'https://other.example.test/auth/v1' })
  refused(token, { keys: verificationKeys(jwks('gregale-0a1b2c3d')), issuer })
  refused(token.slice(0, -4) + 'AAAA', { keys: verificationKeys(keys), issuer })
  refused('not.a.token', { keys: verificationKeys(keys), issuer })
  // A person's token signed with the same key is not a service account's.
  const person = mintToken({ signer, issuer }, account, null, now).split('.')
  const unmarked = { ...payload(person.join('.')), service_account: undefined }
  refused(`${person[0]}.${Buffer.from(JSON.stringify(unmarked)).toString('base64url')}.${person[2]}`, { keys: verificationKeys(keys), issuer })
})

test('the authenticator checks service accounts here and everyone else with GoTrue', async () => {
  const keys = jwks('gregale-0a1b2c3d')
  const account = { id: randomUUID(), secret_id: randomUUID(), org_id: randomUUID(), org_slug: 'acme', role: 'member', permissions: ['org:read'] }
  const token = mintToken({ signer: signingKey(keys), issuer }, account, null)
  const people = []
  const authenticate = createAuthenticator(async value => { people.push(value); return { sub: 'person' } }, value => verifyToken(value, { keys: verificationKeys(keys), issuer }))
  assert.equal((await authenticate(token)).sub, account.id)
  assert.deepEqual(await authenticate('a.b.c'), { sub: 'person' })
  assert.deepEqual(people, ['a.b.c'])
})

test('token requests are limited per account per minute', () => {
  let now = 60_000 * 1000
  const limiter = createRateLimiter(3, () => now, 2)
  assert.deepEqual([limiter('a'), limiter('a'), limiter('a')], [0, 0, 0])
  assert.equal(limiter('a'), 60)
  assert.equal(limiter('b'), 0)
  now += 30_000
  assert.equal(limiter('a'), 30)
  now += 30_000
  assert.equal(limiter('a'), 0)
  // A full map starts over rather than growing.
  assert.equal(limiter('c'), 0)
})

test('issueToken checks the secret against every live one and the scope against the role', async () => {
  const keys = jwks('gregale-0a1b2c3d')
  const signer = signingKey(keys)
  const id = randomUUID()
  const current = newSecret(), next = newSecret()
  const queries = []
  const row = (secret, permissions = ['docs:read', 'docs:write']) => ({ secret_id: randomUUID(), secret_hash: secret.hash, org_id: randomUUID(), org_slug: 'acme', role: 'deployer', permissions })
  let rows = [row(current), row(next)]
  const pool = { query: async (sql, args) => { queries.push([sql, args]); return { rows: sql.includes('service_account_credentials') ? rows : [] } } }
  const issue = (secret, scope = null) => issueToken({ pool, signer, issuer, limiter: () => 0 }, { clientId: id, secret, scope, challenge: {} })
  const answer = await issue(next.secret)
  assert.deepEqual({ ...answer, access_token: undefined }, { access_token: undefined, token_type: 'Bearer', expires_in: limits.accessTokenSeconds })
  assert.equal(payload(answer.access_token).secret_id, rows[1].secret_id)
  assert.match(queries.at(-1)[0], /SET last_used_at = now\(\)/)
  assert.equal(queries.at(-1)[1][0], rows[1].secret_id)
  assert.equal((await issue(current.secret, ['docs:read'])).scope, 'docs:read')
  await assert.rejects(issue(newSecret().secret), { error: 'invalid_client', status: 401 })
  await assert.rejects(issue(current.secret, ['billing:read']), { error: 'invalid_scope', status: 400 })
  rows = [row(current, ['*'])]
  assert.equal((await issue(current.secret, ['billing:read'])).scope, 'billing:read')
  rows = []
  await assert.rejects(issue(current.secret), { error: 'invalid_client' })
  await assert.rejects(issueToken({ pool, signer, issuer, limiter: () => 12 }, { clientId: id, secret: current.secret, scope: null, challenge: {} }),
    err => err.error === 'slow_down' && err.status === 429 && err.headers['Retry-After'] === '12')
  assert.equal(secretHash(current.secret).length, 32)
})

test('a service account\'s token reaches only the permission check and storage on this host', async () => {
  assert.equal(serviceAccountAllowed('POST', '/authz/v1/authorize'), true)
  assert.equal(serviceAccountAllowed('POST', '/authz/v1/storage/sign'), true)
  for (const [method, url] of [['GET', '/auth/v1/user'], ['POST', '/auth/v1/logout'], ['GET', '/authz/v1/orgs'], ['POST', '/authz/v1/authorize?x=1'], ['GET', '/authz/v1/authorize']]) {
    assert.equal(serviceAccountAllowed(method, url), false, `${method} ${url}`)
  }
  const claims = { sub: randomUUID(), service_account: true, secret_id: randomUUID() }
  const pool = { connect: async () => assert.fail('no query expected') }
  for (const [method, path] of [['GET', '/orgs'], ['POST', '/orgs'], ['GET', '/sessions'], ['DELETE', '/account'], ['POST', '/workflows/sync/start']]) {
    await assert.rejects(route(pool, claims, method, path, {}), { code: 'service_account_token' }, `${method} ${path}`)
  }
})

async function listen(server) {
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  return server.address().port
}

test('the front answers client credentials and passes other grants to GoTrue unchanged', async t => {
  const seen = []
  const upstream = http.createServer((req, res) => {
    let body = ''
    req.on('data', chunk => { body += chunk })
    req.on('end', () => {
      seen.push({ url: req.url, body })
      res.writeHead(200, { 'Content-Type': 'application/json' })
      res.end(req.url.startsWith('/.well-known/') ? '{"issuer":"x","grant_types_supported":["authorization_code","refresh_token"]}' : '{"from":"gotrue"}')
    })
  })
  const issued = []
  const serviceAccounts = { issue: async request => { issued.push(request); if (request.secret.endsWith('A')) throw new Error('database down'); return { access_token: 't', token_type: 'Bearer', expires_in: 600 } } }
  const front = createServer({ upstreamPort: await listen(upstream), serviceAccounts, oauth: { location: l => l, clientCredentials: true } })
  const base = `http://127.0.0.1:${await listen(front)}`
  t.after(() => { front.close(); upstream.close() })
  const id = randomUUID()
  const secret = `gasa_${'b'.repeat(43)}`
  const granted = await fetch(`${base}/auth/v1/oauth/token`, { method: 'POST', headers: { 'Content-Type': form, Authorization: basic(id, secret) }, body: 'grant_type=client_credentials' })
  assert.equal(granted.status, 200)
  assert.equal(granted.headers.get('cache-control'), 'no-store')
  assert.deepEqual(await granted.json(), { access_token: 't', token_type: 'Bearer', expires_in: 600 })
  assert.equal(issued[0].clientId, id)
  const wrong = await fetch(`${base}/auth/v1/oauth/token`, { method: 'POST', headers: { 'Content-Type': form, Authorization: basic(id, 'gasa_x') }, body: 'grant_type=client_credentials' })
  assert.equal(wrong.status, 401)
  assert.equal(wrong.headers.get('www-authenticate'), 'Basic realm="service-accounts"')
  assert.deepEqual(await wrong.json(), { error: 'invalid_client' })
  const down = await fetch(`${base}/auth/v1/oauth/token`, { method: 'POST', headers: { 'Content-Type': form }, body: `grant_type=client_credentials&client_id=${id}&client_secret=gasa_${'A'.repeat(43)}` })
  assert.equal(down.status, 503)
  assert.deepEqual(await down.json(), { error: 'temporarily_unavailable' })
  const code = 'grant_type=authorization_code&code=abc&code_verifier=xyz'
  const passed = await fetch(`${base}/auth/v1/oauth/token`, { method: 'POST', headers: { 'Content-Type': form }, body: code })
  assert.deepEqual(await passed.json(), { from: 'gotrue' })
  assert.deepEqual(seen.at(-1), { url: '/oauth/token', body: code })
  const metadata = await (await fetch(`${base}/.well-known/oauth-authorization-server/auth/v1`)).json()
  assert.deepEqual(metadata.grant_types_supported, ['authorization_code', 'refresh_token', 'client_credentials'])
  // A service account's token goes no further than authorize and storage.
  const token = `${Buffer.from('{"alg":"ES256"}').toString('base64url')}.${Buffer.from(JSON.stringify({ sub: id, service_account: true })).toString('base64url')}.sig`
  for (const path of ['/auth/v1/user', '/auth/v1/factors', '/authz/v1/orgs']) {
    const response = await fetch(base + path, { headers: { Authorization: `Bearer ${token}` } })
    assert.equal(response.status, 403, path)
    assert.equal((await response.json()).code, 'service_account_token')
  }
  assert.equal(seen.filter(entry => entry.url === '/user').length, 0)
})

test('the front leaves every grant to GoTrue with service accounts off', async t => {
  const seen = []
  const upstream = http.createServer((req, res) => {
    let body = ''
    req.on('data', chunk => { body += chunk })
    req.on('end', () => { seen.push(body); res.writeHead(400, { 'Content-Type': 'application/json' }); res.end('{"error":"unsupported_grant_type"}') })
  })
  const front = createServer({ upstreamPort: await listen(upstream) })
  const base = `http://127.0.0.1:${await listen(front)}`
  t.after(() => { front.close(); upstream.close() })
  const response = await fetch(`${base}/auth/v1/oauth/token`, { method: 'POST', headers: { 'Content-Type': form }, body: 'grant_type=client_credentials' })
  assert.equal(response.status, 400)
  assert.deepEqual(seen, ['grant_type=client_credentials'])
})

test('managing service accounts checks input and shows a secret once', async () => {
  const org = randomUUID(), account = randomUUID(), secretId = randomUUID()
  const calls = []
  const rows = async (sql, args) => {
    calls.push([sql, args])
    if (sql.includes('create_service_account(')) return [{ id: account, org_id: org, name: args[1], role: args[2], created_at: 'now', secret_id: secretId, expires_at: null }]
    if (sql.includes('create_service_account_secret') || sql.includes('add_service_account_secret')) return [{ id: secretId, service_account_id: account, expires_at: 'later' }]
    if (sql.includes('insert_service_account')) return [{ id: account, org_id: args[0], name: args[1], role: args[2], created_at: 'now', secret_id: secretId, expires_at: null }]
    if (sql.startsWith('DELETE')) return args[0] === account ? [{ id: account }] : []
    return [{ id: account }]
  }
  const [status, made] = await organizationRoute(rows, org, 'POST', [], { name: 'CI deploys', role: 'deployer', expires_in_days: 90 })
  assert.equal(status, 201)
  assert.equal(made.client_id, account)
  assert.match(made.client_secret, /^gasa_/)
  assert.deepEqual(made.secret, { id: secretId, hint: made.client_secret.slice(-4), expires_at: null })
  const [, , , hash, hint, days] = calls.at(-1)[1]
  assert.deepEqual([hash, hint, days], [secretHash(made.client_secret), made.client_secret.slice(-4), 90])
  const [, rotated] = await organizationRoute(rows, org, 'POST', [account, 'secrets'], {})
  assert.equal(rotated.client_id, account)
  assert.equal(rotated.secret.expires_at, 'later')
  assert.equal(calls.at(-1)[1][4], null)
  for (const [method, parts, body, code] of [
    ['POST', [], { name: '', role: 'member' }, 'invalid_name'], ['POST', [], { name: 'x' }, 'invalid_role'], ['POST', [], { name: 'x\n', role: 'member' }, 'invalid_name'],
    ['POST', [], { name: 'x', role: 'member', disabled: true }, 'invalid_service_account'], ['POST', [], { name: 'x', role: 'member', expires_in_days: 0 }, 'invalid_expires_in_days'],
    ['POST', [], { name: 'x', role: 'member', expires_in_days: limits.serviceAccountSecretMaxDays + 1 }, 'invalid_expires_in_days'],
    ['PATCH', [account], {}, 'invalid_service_account'], ['PATCH', [account], { disabled: 'yes' }, 'invalid_disabled'], ['PATCH', ['nope'], { name: 'x' }, 'invalid_id'],
    ['DELETE', [account, 'secrets', 'nope'], undefined, 'invalid_id'], ['GET', [account, 'other'], undefined, 'not_found'],
  ]) await assert.rejects(organizationRoute(rows, org, method, parts, body), { code }, `${method} ${parts} ${JSON.stringify(body)}`)
  assert.deepEqual((await organizationRoute(rows, org, 'PATCH', [account], { disabled: true }))[0], 200)
  assert.deepEqual(calls.at(-1)[1], [org, account, null, null, true])
  assert.deepEqual(await organizationRoute(rows, org, 'DELETE', [account, 'secrets', secretId], undefined), [204, null])
  // The operator names the organization and may delete any account.
  await assert.rejects(adminRoute(rows, 'POST', [], new URLSearchParams(), { name: 'x', role: 'member' }), { code: 'invalid_org' })
  const [, byOperator] = await adminRoute(rows, 'POST', [], new URLSearchParams(), { org, name: 'Operator', role: 'owner' })
  assert.equal(byOperator.org_id, org)
  assert.deepEqual(calls.at(-1)[1].slice(0, 3), [org, 'Operator', 'owner'])
  await assert.rejects(adminRoute(rows, 'GET', [], new URLSearchParams('org=nope')), { code: 'invalid_id' })
  await adminRoute(rows, 'GET', [], new URLSearchParams(`org=${org}`))
  assert.deepEqual(calls.at(-1)[1], [org, limits.adminPageSize])
  assert.deepEqual(await adminRoute(rows, 'DELETE', [account], new URLSearchParams()), [204, null])
  await assert.rejects(adminRoute(rows, 'DELETE', [randomUUID()], new URLSearchParams()), { code: 'not_found' })
})
