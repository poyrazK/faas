import { readFile } from 'node:fs/promises'
import { isDeepStrictEqual } from 'node:util'
import { fileURLToPath, pathToFileURL } from 'node:url'

const bodyLimit = 1024 * 1024
const tokenShape = /^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/

// Compare decimal values, not their spelling, while retaining enough precision
// to detect a JavaScript number that was rounded by the SDK's default mode.
function decimalKey(source) {
  const match = /^(-?)([0-9]+)(?:\.([0-9]+))?(?:[eE]([+-]?[0-9]+))?$/.exec(source)
  if (!match) return null
  let digits = (match[2] + (match[3] ?? '')).replace(/^0+/, '')
  if (!digits) return '0'
  let exponent = Number(match[4] ?? '0') - (match[3]?.length ?? 0)
  if (!Number.isSafeInteger(exponent)) return null
  while (digits.endsWith('0')) { digits = digits.slice(0, -1); exponent++ }
  return `${match[1]}${digits}e${exponent}`
}
function sameExactValues(a, b, exactPrototype) {
  const isExact = value => value && typeof value === 'object' && Object.getPrototypeOf(value) === exactPrototype && typeof value.source === 'string'
  if (isExact(a) || isExact(b)) return Boolean(isExact(a) && isExact(b) && decimalKey(a.source) !== null && decimalKey(a.source) === decimalKey(b.source))
  if (Object.is(a, b)) return true
  if (!a || !b || typeof a !== 'object' || typeof b !== 'object' || Array.isArray(a) !== Array.isArray(b)) return false
  const keys = Object.keys(a)
  return keys.length === Object.keys(b).length && keys.every(key => Object.hasOwn(b, key) && sameExactValues(a[key], b[key], exactPrototype))
}
export async function verifyHostedIntegration({ origin, fingerprint, relation, numericMode, expectedText, token, createHostedData, parseExactJSON, parseLosslessJSON, fetcher = fetch }) {
  const base = new URL(origin)
  if (base.protocol !== 'https:' || base.username || base.password || base.search || base.hash || base.pathname !== '/' || !/^[a-f0-9]{64}$/.test(fingerprint) || !/^[A-Za-z_][A-Za-z0-9_]{0,62}$/.test(relation) || !['number', 'exact', 'lossless'].includes(numericMode)) throw new Error('Invalid hosted verification target')
  if (typeof token !== 'string' || token.length > 16384 || !tokenShape.test(token)) throw new Error('An application JWT is required')
  const expected = numericMode === 'exact' ? parseExactJSON(expectedText) : numericMode === 'lossless' ? parseLosslessJSON(expectedText) : JSON.parse(expectedText)
  if (!Array.isArray(expected) || expected.length > 100) throw new Error('Expected JSON must be an array of at most 100 rows')
  let requests = 0
  const boundedFetch = async (input, init = {}) => {
    const target = new URL(typeof input === 'string' || input instanceof URL ? input : input.url)
    const headersIn = new Headers(init.headers ?? (input instanceof Request ? input.headers : undefined))
    const method = init.method ?? (input instanceof Request ? input.method : 'GET')
    if (target.origin !== base.origin || target.pathname !== `/__gregale/contract/${fingerprint}/rest/v1/${relation}` || method !== 'GET' || headersIn.get('accept-profile') !== 'public' || headersIn.get('authorization') !== `Bearer ${token}` || target.searchParams.get('limit') !== '100' || ++requests !== 1) throw new Error('Unexpected verification request')
    const response = await fetcher(target, { ...init, redirect: 'error', signal: AbortSignal.timeout(30000) })
    const chunks = []; let size = 0
    if (response.body) {
      const reader = response.body.getReader()
      try { for (;;) { const { done, value } = await reader.read(); if (done) break; size += value.length; if (size > bodyLimit) throw new Error('Response exceeds verification limit'); chunks.push(value) } }
      finally { await reader.cancel().catch(() => {}); reader.releaseLock() }
    }
    const headers = new Headers(response.headers); headers.delete('content-length'); headers.delete('content-encoding')
    return new Response(Buffer.concat(chunks), { status: response.status, statusText: response.statusText, headers })
  }
  const client = createHostedData({ url: base.origin, accessToken: () => token, fetch: boundedFetch })
  const result = await client.read()
  let matches = isDeepStrictEqual(result.data, expected)
  if (matches && numericMode === 'number') {
    const sample = parseExactJSON('0')
    if (!sample || typeof sample !== 'object' || typeof sample.source !== 'string') throw new Error('Exact-number verification is unavailable')
    matches = sameExactValues(parseExactJSON(expectedText), parseExactJSON(JSON.stringify(result.data)), Object.getPrototypeOf(sample))
  }
  if (requests !== 1 || result.error || !matches) throw new Error('Hosted read did not match the supplied assertion')
}

async function main() {
  const options = JSON.parse(await readFile(process.argv[2], 'utf8'))
  const expectedText = await readFile(options.assertion, 'utf8')
  const { createHostedData } = await import('./client.js')
  const sdk = await import('@gregale/data')
  await verifyHostedIntegration({ ...options, expectedText, token: process.env.GREGALE_INTEGRATION_TOKEN, createHostedData, parseExactJSON: sdk.parseExactJSON, parseLosslessJSON: sdk.parseLosslessJSON })
  process.stdout.write('Hosted typed read matched the supplied assertion.\n')
}
if (process.argv[1] && pathToFileURL(process.argv[1]).href === pathToFileURL(fileURLToPath(import.meta.url)).href) main().catch(() => { process.stderr.write('Hosted typed read failed; check the JWT, API availability and expected rows.\n'); process.exitCode = 1 })
