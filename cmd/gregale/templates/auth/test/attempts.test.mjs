import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { once } from 'node:events'
import { createServer } from '../server.mjs'
import { budgets, guardedPath, outcome, passwordEntries, secondStepEntries, signInSubject } from '../attempts.mjs'
import { limits } from '../config.mjs'

// memoryAttempts stands in for the database budgets with the same contract.
function memoryAttempts() {
  const counts = new Map()
  const id = key => key.toString('hex')
  return {
    counts,
    async reserve(entries) {
      let over = false
      for (const { key, budget } of entries) {
        counts.set(id(key), (counts.get(id(key)) ?? 0) + 1)
        if (counts.get(id(key)) > budget.limit) over = true
      }
      return over ? budgets.passwordAccount.seconds : 0
    },
    async release(entries) { for (const { key } of entries) counts.set(id(key), Math.max(0, (counts.get(id(key)) ?? 0) - 1)) },
    async clear(entries) { for (const { key } of entries) counts.delete(id(key)) },
  }
}

async function listen(server) {
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  return server.address().port
}

// The fake GoTrue accepts the password "right" and the code "123456".
async function harness(t, attempts) {
  const seen = []
  const upstream = http.createServer((req, res) => {
    let body = ''
    req.on('data', chunk => { body += chunk })
    req.on('end', () => {
      seen.push(req.url)
      const parsed = JSON.parse(body || '{}')
      res.setHeader('Content-Type', 'application/json')
      if (req.url.startsWith('/token')) {
        if (parsed.password === 'right') return res.end('{"access_token":"t"}')
        if (parsed.password === undefined) {
          res.statusCode = 400
          return res.end('{"code":400,"error_code":"validation_failed","msg":"missing password"}')
        }
        res.statusCode = 400
        return res.end('{"code":400,"error_code":"invalid_credentials","msg":"Invalid login credentials"}')
      }
      if (parsed.code === '123456') return res.end('{"access_token":"t2"}')
      res.statusCode = 422
      res.end('{"code":422,"error_code":"mfa_verification_failed","msg":"Invalid TOTP code entered"}')
    })
  })
  const front = createServer({ upstreamPort: await listen(upstream), attempts })
  const port = await listen(front)
  t.after(() => { front.close(); upstream.close() })
  return { base: `http://127.0.0.1:${port}`, seen }
}

const signIn = (base, body, address = '203.0.113.7') => fetch(`${base}/auth/v1/token?grant_type=password`, {
  method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Real-IP': address }, body: JSON.stringify(body),
})

const user = '11111111-2222-4333-8444-555555555555'
const bearer = `Bearer ${Buffer.from('{"alg":"ES256"}').toString('base64url')}.${Buffer.from(JSON.stringify({ sub: user, session_id: user })).toString('base64url')}.sig`

test('names the account a password sign-in is for, as GoTrue reads it', () => {
  assert.equal(signInSubject(Buffer.from('{"email":"Ann@Example.com","password":"x"}')), 'email:ann@example.com')
  assert.equal(signInSubject(Buffer.from('{"EMAIL":"ann@example.com"}')), 'email:ann@example.com')
  assert.equal(signInSubject(Buffer.from('{"phone":"+1 555 0100"}')), 'phone:15550100')
  assert.equal(signInSubject(Buffer.from('{"password":"x"}')), null)
  assert.throws(() => signInSubject(Buffer.from('{"email":"a@example.com","Email":"b@example.com"}')), /ambiguous/)
  assert.throws(() => signInSubject(Buffer.from('email=a@example.com')), /invalid/)
  assert.throws(() => signInSubject(Buffer.from('[]')), /invalid/)
})

test('budgets password sign-ins and second-step codes only', () => {
  assert.equal(guardedPath('POST', '/token?grant_type=password'), 'password')
  assert.equal(guardedPath('POST', '/token/?grant_type=password'), 'password')
  assert.equal(guardedPath('POST', '/token?grant_type=refresh_token'), null)
  assert.equal(guardedPath('POST', '/token?grant_type=refresh_token&grant_type=password'), null)
  assert.equal(guardedPath('POST', `/factors/${user}/verify`), 'second-step')
  assert.equal(guardedPath('POST', '/factors/recovery-codes/verify/'), 'second-step')
  assert.equal(guardedPath('POST', `/factors/${user}/challenge`), null)
  assert.equal(guardedPath('GET', '/token?grant_type=password'), null)
  assert.equal(outcome('password', 400, { error_code: 'invalid_credentials' }), 'failure')
  assert.equal(outcome('password', 400, { error_code: 'validation_failed' }), null)
  assert.equal(outcome('second-step', 422, { error_code: 'mfa_verification_failed' }), 'failure')
  assert.equal(outcome('password', 200, {}), 'success')
  assert.equal(passwordEntries('email:a@example.com', '203.0.113.7').length, 2)
  assert.deepEqual(secondStepEntries({ sub: 'not-a-uuid' }), [])
})

test('wrong passwords from one address stop at that address only', async t => {
  const attempts = memoryAttempts()
  const { base, seen } = await harness(t, attempts)
  for (let i = 0; i < limits.passwordFailuresPerAddress; i++) {
    assert.equal((await signIn(base, { email: 'ann@example.com', password: 'wrong' })).status, 400)
  }
  const held = await signIn(base, { email: 'ann@example.com', password: 'right' })
  assert.equal(held.status, 429)
  assert.ok(Number(held.headers.get('retry-after')) > 0)
  assert.equal((await held.json()).error_code, 'too_many_attempts')
  assert.equal(seen.length, limits.passwordFailuresPerAddress, 'a held attempt never reaches GoTrue')
  // The owner elsewhere still signs in; the guessing address stays held
  // until its window ends, whatever the case of the address it names.
  assert.equal((await signIn(base, { email: 'ann@example.com', password: 'right' }, '198.51.100.9')).status, 200)
  assert.equal((await signIn(base, { email: 'Ann@Example.com', password: 'right' })).status, 429)
})

test('guessing one account from many addresses is held per account', async t => {
  const attempts = memoryAttempts()
  const { base } = await harness(t, attempts)
  for (let i = 0; i < limits.passwordFailuresPerAccount; i++) {
    assert.equal((await signIn(base, { email: 'ann@example.com', password: 'wrong' }, `192.0.2.${i}`)).status, 400)
  }
  assert.equal((await signIn(base, { email: 'ann@example.com', password: 'right' }, '198.51.100.9')).status, 429)
  assert.equal((await signIn(base, { email: 'bob@example.com', password: 'right' }, '198.51.100.9')).status, 200)
})

test('a burst of parallel guesses cannot outrun the budget', async t => {
  const { base, seen } = await harness(t, memoryAttempts())
  const answers = await Promise.all(Array.from({ length: 30 }, () => signIn(base, { email: 'ann@example.com', password: 'wrong' })))
  assert.equal(answers.filter(response => response.status === 429).length, 30 - limits.passwordFailuresPerAddress)
  assert.equal(seen.length, limits.passwordFailuresPerAddress)
})

test('attempts GoTrue refuses for another reason are given back', async t => {
  const { base } = await harness(t, memoryAttempts())
  for (let i = 0; i < limits.passwordFailuresPerAddress + 5; i++) {
    assert.equal((await signIn(base, { email: 'ann@example.com' })).status, 400)
  }
  assert.equal((await signIn(base, { email: 'ann@example.com', password: 'right' })).status, 200)
})

test('refuses sign-in bodies GoTrue could read differently', async t => {
  const { base, seen } = await harness(t, memoryAttempts())
  const twice = await fetch(`${base}/auth/v1/token?grant_type=password`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{"email":"a@example.com","Email":"b@example.com","password":"x"}' })
  assert.equal(twice.status, 400)
  const form = await fetch(`${base}/auth/v1/token?grant_type=password`, { method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, body: 'email=a@example.com&password=x' })
  assert.equal(form.status, 400)
  assert.equal(seen.length, 0)
})

test('wrong second-step codes are held per user', async t => {
  const { base } = await harness(t, memoryAttempts())
  const verify = code => fetch(`${base}/auth/v1/factors/${user}/verify`, { method: 'POST', headers: { 'Content-Type': 'application/json', Authorization: bearer }, body: JSON.stringify({ challenge_id: user, code }) })
  for (let i = 0; i < limits.secondStepFailuresPerUser; i++) assert.equal((await verify('000000')).status, 422)
  assert.equal((await verify('123456')).status, 429)
})

test('a budget store that fails refuses rather than lets attempts through', async t => {
  const failing = { reserve: async () => { throw Object.assign(new Error('down'), { code: '57P01' }) }, release: async () => {}, clear: async () => {} }
  const quiet = console.error
  console.error = () => {}
  t.after(() => { console.error = quiet })
  const { base, seen } = await harness(t, failing)
  assert.equal((await signIn(base, { email: 'ann@example.com', password: 'right' })).status, 503)
  assert.equal(seen.length, 0)
})

test('the image copies every module the front loads', async () => {
  const { readdir, readFile } = await import('node:fs/promises')
  const root = new URL('../', import.meta.url)
  const dockerfile = await readFile(new URL('Dockerfile', root), 'utf8')
  const copied = new Set(dockerfile.split('\n').filter(line => line.startsWith('COPY ') && line.includes('.mjs')).flatMap(line => line.split(/\s+/)))
  const modules = (await readdir(root)).filter(name => name.endsWith('.mjs'))
  assert.deepEqual(modules.filter(name => !copied.has(name)), [])
})
