import test from 'node:test'
import assert from 'node:assert/strict'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'

const sdkSource = process.env.DATA_API_SDK_ENTRY ? pathToFileURL(process.env.DATA_API_SDK_ENTRY) : new URL('../../sdk/data/dist/index.js', import.meta.url)
const { createDataClient, parseExactJSON, parseLosslessJSON } = await import(sdkSource)

const source = process.env.DATA_API_RUNTIME_DIR ? pathToFileURL(join(process.env.DATA_API_RUNTIME_DIR, 'hosted-integration-verify.mjs')) : new URL('../../cmd/gregale/templates/data-api/hosted-integration-verify.mjs', import.meta.url)
const { verifyHostedIntegration } = await import(source)

for (const numericMode of ['exact', 'lossless']) test(`hosted typed SDK read verifies ${numericMode} integers without rounding`, async () => {
  const fingerprint = 'a'.repeat(64), expectedText = '[{"id":9007199254740993}]'
  const createHostedData = options => {
    const client = createDataClient({ ...options, numericMode, expectedSchemaFingerprint: fingerprint }).schema('public')
    return { read: () => client.from('notes').select('*').order('id').limit(100) }
  }
  await verifyHostedIntegration({ origin: 'https://api.example/', fingerprint, relation: 'notes', numericMode, expectedText, token: 'a.eyJzdWIiOiJhbGljZSJ9.signature', createHostedData, parseExactJSON, parseLosslessJSON, fetcher: async () => new Response(expectedText, { headers: { 'content-type': 'application/json' } }) })
})

test('number mode cannot pass an assertion after integer or decimal rounding', async () => {
  const fingerprint = 'a'.repeat(64)
  const createHostedData = options => {
    const client = createDataClient({ ...options, numericMode: 'number', expectedSchemaFingerprint: fingerprint }).schema('public')
    return { read: () => client.from('notes').select('*').limit(100) }
  }
  for (const expectedText of ['[{"id":9007199254740993}]', '[{"id":0.1234567890123456789}]']) {
    await assert.rejects(verifyHostedIntegration({ origin: 'https://api.example/', fingerprint, relation: 'notes', numericMode: 'number', expectedText, token: 'a.eyJzdWIiOiJhbGljZSJ9.signature', createHostedData, parseExactJSON, parseLosslessJSON, fetcher: async () => new Response(expectedText, { headers: { 'content-type': 'application/json' } }) }))
  }
})
