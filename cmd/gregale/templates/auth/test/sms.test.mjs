import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { once } from 'node:events'
import { createServer } from '../server.mjs'
import { bodyAllowed, phoneAllowed, smsCountries, smsSendPath } from '../sms.mjs'
import { limits } from '../config.mjs'

test('the allowlist is a short list of country calling codes', () => {
  assert.equal(smsCountries({}), null)
  assert.equal(smsCountries({ AUTH_SMS_COUNTRIES: '' }), null)
  assert.deepEqual(smsCountries({ AUTH_SMS_COUNTRIES: '1,44,49,44' }), ['1', '44', '49'])
  for (const bad of ['+44', '44 ', '0', '1234', 'uk', '1,,44', Array.from({ length: limits.smsCountries + 1 }, (_, i) => String(i + 1)).join(',')]) {
    assert.throws(() => smsCountries({ AUTH_SMS_COUNTRIES: bad }), /AUTH_SMS_COUNTRIES/, bad)
  }
})

test('numbers are normalised as GoTrue does before the country check', () => {
  const uk = ['44']
  assert.equal(phoneAllowed('+447700900123', uk), true)
  assert.equal(phoneAllowed('+44 7700 900123', uk), true)
  assert.equal(phoneAllowed('447700900123', uk), true)
  assert.equal(phoneAllowed('+12025550123', uk), false)
  assert.equal(phoneAllowed('+881 6 1234 5678', uk), false)
  // GoTrue refuses these itself, so none is ever texted.
  assert.equal(phoneAllowed('+44-7700-900123', uk), true)
  assert.equal(phoneAllowed('0044 7700 900123', uk), true)
  assert.equal(phoneAllowed(447700900123, uk), true)
})

test('every key GoTrue would read as the phone is checked', () => {
  const uk = ['44']
  assert.equal(bodyAllowed(Buffer.from('{"phone":"+447700900123","channel":"sms"}'), uk), true)
  assert.equal(bodyAllowed(Buffer.from('{"phone":"+12025550123"}'), uk), false)
  // Go's JSON decoding matches field names without regard to case.
  assert.equal(bodyAllowed(Buffer.from('{"phone":"+447700900123","PHONE":"+12025550123"}'), uk), false)
  assert.equal(bodyAllowed(Buffer.from('{"Phone":"+12025550123"}'), uk), false)
  assert.equal(bodyAllowed(Buffer.from('{"email":"a@example.com"}'), uk), true)
  assert.equal(bodyAllowed(Buffer.from('not json'), uk), true)
})

test('only requests that can text a number are checked', () => {
  for (const [method, url, want] of [
    ['POST', '/auth/v1/otp', true], ['POST', '/auth/v1/otp?redirect_to=x', true], ['POST', '/auth/v1/signup', true],
    ['POST', '/auth/v1/resend', true], ['POST', '/auth/v1/factors', true], ['PUT', '/auth/v1/user', true],
    ['GET', '/auth/v1/user', false], ['POST', '/auth/v1/verify', false], ['POST', '/auth/v1/token?grant_type=password', false],
  ]) {
    assert.equal(smsSendPath(method, url), want, `${method} ${url}`)
  }
})

async function listen(server) {
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  return server.address().port
}

test('the front refuses numbers outside the allowlist before GoTrue sees them', async t => {
  const seen = []
  const upstream = http.createServer((req, res) => {
    let body = ''
    req.on('data', chunk => { body += chunk })
    req.on('end', () => { seen.push({ url: req.url, body, length: req.headers['content-length'] }); res.writeHead(200, { 'Content-Type': 'application/json' }); res.end('{}') })
  })
  const front = createServer({ upstreamPort: await listen(upstream), smsCountries: ['44'], bodyBytes: 1024 })
  const base = `http://127.0.0.1:${await listen(front)}`
  t.after(() => { front.close(); upstream.close() })
  const post = (path, body, method = 'POST') => fetch(base + path, { method, headers: { 'Content-Type': 'application/json' }, body })

  const refused = await post('/auth/v1/otp', '{"phone":"+12025550123","channel":"sms"}')
  assert.equal(refused.status, 422)
  assert.deepEqual(await refused.json(), { status: 422, code: 'sms_country_not_allowed' })
  assert.equal((await post('/auth/v1/user', '{"phone":"+12025550123"}', 'PUT')).status, 422)
  assert.equal(seen.length, 0)

  const body = '{"phone":"+44 7700 900123","create_user":true}'
  assert.equal((await post('/auth/v1/otp?redirect_to=https%3A%2F%2Fshop.example.com', body)).status, 200)
  assert.deepEqual(seen.at(-1), { url: '/otp?redirect_to=https%3A%2F%2Fshop.example.com', body, length: String(Buffer.byteLength(body)) })
  // Other requests stream through unchecked.
  assert.equal((await post('/auth/v1/token?grant_type=password', '{"phone":"+12025550123","password":"x"}')).status, 200)
  assert.equal(seen.length, 2)
  assert.equal((await post('/auth/v1/otp', JSON.stringify({ phone: '+447700900123', pad: 'x'.repeat(2000) }))).status, 413)
})
