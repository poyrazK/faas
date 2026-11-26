// Exercises the actual hosted replay with a generated connection collection.
import assert from 'node:assert/strict'
import { runHostedReplay } from '../../cmd/gregale/templates/data-api/hosted-replay.mjs'

const token = sub => `e30.${Buffer.from(JSON.stringify({sub})).toString('base64url')}.signature`
const tokens = { alice: token('alice'), bob: token('bob') }
const env = { ALICE_JWT: tokens.alice, BOB_JWT: tokens.bob }
const options = { collection: process.argv[2], identities: {alice:'ALICE_JWT',bob:'BOB_JWT'}, scenarios: [], read_only: true, url: 'https://example.test/', fingerprint: 'a'.repeat(64) }

function makeFetcher(leak, missing = false) {
  return async (url, init) => {
    if (url.pathname === '/openapi.json') return new Response('{}', {status: 401})
    if (url.pathname === '/__gregale/schema') return Response.json({version:1,ready:true,fingerprint:'a'.repeat(64)})
    assert.equal(init.method, 'GET')
    assert.match(url.pathname, /^\/__gregale\/contract\/a{64}\/rest\/v1\/notes$/)
    assert.equal(url.searchParams.get('select'), 'id')
    assert.equal(url.searchParams.get('limit'), '2')
    const id = Number(url.searchParams.get('id').replace('eq.', ''))
    const user = init.headers.Authorization === `Bearer ${tokens.alice}` ? 'alice' : 'bob'
    const owner = id === 101 ? 'alice' : 'bob'
    if (missing && id === 101) return Response.json([])
    return Response.json(leak || user === owner ? [{id}] : [])
  }
}

const events = []
const success = await runHostedReplay(options, env, {fetcher:makeFetcher(false),emit:event=>events.push(event)})
assert.equal(success.failed, 0)
assert.equal(events.filter(e => e.event === 'replay_result' && e.scenario === 'default').length, 4)
assert.doesNotMatch(JSON.stringify(events), /signature|101|202/)
await assert.rejects(runHostedReplay(options, env, {fetcher:makeFetcher(true),emit:()=>{}}), /verification failed/)
await assert.rejects(runHostedReplay(options, env, {fetcher:makeFetcher(false, true),emit:()=>{}}), /verification failed/)
