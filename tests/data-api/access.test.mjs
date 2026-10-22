import test from 'node:test'
import assert from 'node:assert/strict'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
const runtime = process.env.DATA_API_RUNTIME_DIR
const access = runtime ? await import(pathToFileURL(join(runtime, 'dev-access.mjs'))) : null
test('access assertions require JSON and identities; failed scenarios stop without exposing values', { skip: !runtime }, async t => {
 const dir = await mkdtemp(join(tmpdir(), 'gregale-access-unit-')); t.after(() => rm(dir, { recursive: true, force: true }))
 const path = join(dir, 'access.json'), project = { identities: { alice: {} } }
 const request = { name: 'Read', identity: 'alice', method: 'GET', path: '/rest/v1/notes', expect: { status: 200, json: [] } }
 await writeFile(path, JSON.stringify({ version: 1, requests: [request] }))
 const scenarios = await access.prepareAccessChecks(path, project)
 let calls = 0
 scenarios[0].requests.push({ ...request, name: 'Blocked' })
 const results = await access.runAccessChecks(scenarios, { url: 'http://127.0.0.1', identities: { alice: { token: 'secret' } }, signal: new AbortController().signal, fetcher: async () => { calls++; return new Response(JSON.stringify([{ private: 'sensitive' }]), { status: 200 }) } })
 assert.equal(calls, 1); assert.equal(results[0].passed, false); assert.equal(results[1].skipped, true); assert.ok(!JSON.stringify(results).includes('sensitive'))
 for (const requests of [[], [{ ...request, identity: 'missing' }], [{ ...request, expect: { status: 200 } }]]) {
  await writeFile(path, JSON.stringify({ version: 1, requests })); await assert.rejects(access.prepareAccessChecks(path, project))
 }
})
