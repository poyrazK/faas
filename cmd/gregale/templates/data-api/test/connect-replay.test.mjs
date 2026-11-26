import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { prepareHostedReplay, runHostedReplay } from '../hosted-replay.mjs'

const token = sub => `e30.${Buffer.from(JSON.stringify({ sub })).toString('base64url')}.c2ln`
const env = { ALICE_JWT: token('alice'), BOB_JWT: token('bob') }
const request = (identity, overrides = {}) => ({ name: identity, method: 'GET', path: '/rest/v1/notes?select=id', identity, expect: { status: 200, json: [] }, ...overrides })

async function prepare(requests, options = {}) {
  const dir = await mkdtemp(join(tmpdir(), 'connect-replay-test-'))
  try {
    const collection = join(dir, 'requests.json')
    await writeFile(collection, JSON.stringify({ version: 1, requests }))
    return await prepareHostedReplay({ collection, identities: { alice: 'ALICE_JWT', bob: 'BOB_JWT' }, scenarios: [], read_only: true, ...options }, env)
  } finally { await rm(dir, { recursive: true, force: true }) }
}

test('connection accepts two subjects with explicit expected rows', async () => {
  const result = await prepare([request('alice'), request('bob')])
  assert.equal(result.scenarios[0].requests.length, 2)
})

test('connection rejects writes, RPC and status-only checks', async () => {
  for (const bad of [
    request('bob', { method: 'POST', body: {} }),
    request('bob', { path: '/rest/v1/rpc/mutate' }),
    request('bob', { path: '/rest/v1/%72pc/mutate' }),
    request('bob', { expect: { status: 200 } }),
    request('bob', { expect: { status: 403, json: {} } }),
  ]) await assert.rejects(prepare([request('alice'), bad]))
})

test('connection rejects two identities with the same subject', async () => {
  await assert.rejects(prepare([request('alice'), request('bob')], { identities: { alice: 'ALICE_JWT', bob: 'ALICE_JWT' } }))
})

test('hosted connection checks both identities and pins every query to the exported schema', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'connect-replay-test-'))
  try {
    const collection = join(dir, 'requests.json')
    await writeFile(collection, JSON.stringify({ version: 1, requests: [request('alice'), request('bob')] }))
    const fingerprint = 'a'.repeat(64)
    const seen = []
    const fetcher = async (url, init) => {
      seen.push(url.pathname)
      if (url.pathname === '/openapi.json') return new Response('', { status: 401 })
      if (url.pathname === '/__gregale/schema') return Response.json({ version: 1, ready: true, fingerprint })
      assert.equal(url.pathname, `/__gregale/contract/${fingerprint}/rest/v1/notes`)
      assert.ok([`Bearer ${env.ALICE_JWT}`, `Bearer ${env.BOB_JWT}`].includes(init.headers.Authorization))
      return Response.json([])
    }
    const options = { collection, identities: { alice: 'ALICE_JWT', bob: 'BOB_JWT' }, scenarios: [], read_only: true, url: 'https://example.com/', fingerprint }
    const report = await runHostedReplay(options, env, { fetcher, emit: () => {} })
    assert.equal(report.failed, 0)
    assert.equal(seen.filter(path => path.includes('/rest/v1/notes')).length, 2)
    await assert.rejects(runHostedReplay(options, env, { fetcher: async (url, init) => url.pathname === '/__gregale/schema' ? Response.json({ version: 1, ready: true, fingerprint: 'b'.repeat(64) }) : fetcher(url, init), emit: () => {} }))
  } finally { await rm(dir, { recursive: true, force: true }) }
})
