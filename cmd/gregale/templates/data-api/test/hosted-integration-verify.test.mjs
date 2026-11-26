import test from 'node:test'
import assert from 'node:assert/strict'
import { verifyHostedIntegration } from '../hosted-integration-verify.mjs'

const fingerprint = 'a'.repeat(64), token = 'a.eyJzdWIiOiJhbGljZSJ9.signature'
class ExactNumber { constructor(source) { this.source = source } }
const parseExactJSON = source => JSON.parse(source, (_key, value) => typeof value === 'number' ? new ExactNumber(String(value)) : value)
const base = { origin: 'https://api.example/', fingerprint, relation: 'notes', numericMode: 'number', expectedText: '[{"id":1}]', token, parseExactJSON, parseLosslessJSON: JSON.parse }
function clientFactory(path = `/__gregale/contract/${fingerprint}/rest/v1/notes?select=*&limit=100`) {
  return ({ fetch }) => ({ read: async () => {
    const response = await fetch(`https://api.example${path}`, { method: 'GET', headers: { Authorization: `Bearer ${token}`, 'Accept-Profile': 'public' } })
    return { data: await response.json(), error: response.ok ? null : { status: response.status } }
  } })
}
test('verified client performs one fingerprint-pinned read and compares actual rows', async () => {
  let calls = 0
  await verifyHostedIntegration({ ...base, createHostedData: clientFactory(), fetcher: async (url, init) => {
    calls++; assert.equal(url.origin, 'https://api.example'); assert.equal(init.redirect, 'error'); assert.equal(init.headers.Authorization, `Bearer ${token}`)
    return Response.json([{ id: 1 }])
  } })
  assert.equal(calls, 1)
  await assert.rejects(verifyHostedIntegration({ ...base, createHostedData: clientFactory(), fetcher: async () => Response.json([{ id: 2 }]) }))
})
test('verification refuses changed routes, writes, invalid tokens and oversized responses', async () => {
  let calls = 0
  const fetcher = async () => { calls++; return Response.json([{ id: 1 }]) }
  for (const path of ['/rest/v1/notes?limit=100', `/__gregale/contract/${'b'.repeat(64)}/rest/v1/notes?limit=100`, `/__gregale/contract/${fingerprint}/rest/v1/notes?limit=101`]) await assert.rejects(verifyHostedIntegration({ ...base, createHostedData: clientFactory(path), fetcher }))
  await assert.rejects(verifyHostedIntegration({ ...base, token: 'account-key', createHostedData: clientFactory(), fetcher }))
  await assert.rejects(verifyHostedIntegration({ ...base, createHostedData: ({ fetch }) => ({ read: () => fetch(`https://api.example/__gregale/contract/${fingerprint}/rest/v1/notes?limit=100`, { method: 'POST' }) }), fetcher }))
  assert.equal(calls, 0)
  await assert.rejects(verifyHostedIntegration({ ...base, createHostedData: clientFactory(), fetcher: async () => new Response('x'.repeat(1024 * 1024 + 1)) }))
})
