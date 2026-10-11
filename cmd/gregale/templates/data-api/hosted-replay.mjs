import { readFile } from 'node:fs/promises'
import { pathToFileURL } from 'node:url'
import { createHash } from 'node:crypto'
import { requestStore } from './dev-requests.mjs'
import { replayRequest, collectionScenarios } from './request-replay.mjs'

const identifier = /^[A-Za-z][A-Za-z0-9_-]{0,63}$/
const envName = /^[A-Za-z_][A-Za-z0-9_]*$/
const jwt = /^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/
const fingerprint = /^[a-f0-9]{64}$/

export async function prepareHostedReplay(options, env = process.env) {
  const mappings = options.identities
  if (!mappings || Array.isArray(mappings) || typeof mappings !== 'object' || Object.keys(mappings).length < 2 || Object.keys(mappings).length > 100) throw new Error('Configure two to 100 environment-mapped identities.')
  const identities = Object.fromEntries(Object.entries(mappings).map(([name, variable]) => {
    if (!identifier.test(name) || ['constructor', 'prototype', '__proto__'].includes(name) || typeof variable !== 'string' || !envName.test(variable)) throw new Error('Use named identities mapped to JWT environment variables.')
    const token = env[variable]
    if (typeof token !== 'string' || token.length > 16384 || !jwt.test(token)) throw new Error('Configure each identity with an application JWT; never use an account key.')
    let claims
    try { claims = JSON.parse(Buffer.from(token.split('.')[1], 'base64url').toString('utf8')) } catch { throw new Error('Identity JWT payload is invalid.') }
    if (typeof claims?.sub !== 'string' || !claims.sub || claims.sub.length > 256) throw new Error('Each identity JWT needs a nonempty subject.')
    return [name, { token, subject: claims.sub }]
  }))
  if (options.fingerprint && !fingerprint.test(options.fingerprint)) throw new Error('Invalid verification fingerprint.')
  if (!envName.test(options.refresh_token_env ?? 'GREGALE_DATA_API_REFRESH_TOKEN')) throw new Error('Invalid refresh token environment variable.')
  const refreshToken = env[options.refresh_token_env ?? 'GREGALE_DATA_API_REFRESH_TOKEN'] || ''
  if (refreshToken && !fingerprint.test(refreshToken)) throw new Error('Invalid private refresh token.')
  const collection = await requestStore(options.collection, { identities: Object.keys(identities) }).read(true)
  if (!Array.isArray(options.scenarios) || new Set(options.scenarios).size !== options.scenarios.length || options.scenarios.some(name => typeof name !== 'string' || !identifier.test(name))) throw new Error('Use unique scenario identifiers.')
  const scenarios = options.scenarios.length ? options.scenarios.flatMap(name => collectionScenarios(collection, name)) : collectionScenarios(collection)
  const requests = scenarios.flatMap(scenario => scenario.requests)
  if (!requests.length || new Set(requests.map(request => identities[request.identity].subject)).size < 2) throw new Error('Hosted authorization verification must exercise two distinct subjects with nonempty scenarios.')
  if (options.fingerprint && requests.some(request => request.expectedSchemaFingerprint && request.expectedSchemaFingerprint !== options.fingerprint)) throw new Error('Required authorization scenarios must target the verified candidate fingerprint.')
  const secrets = [refreshToken, refreshToken ? createHash('sha256').update(refreshToken).digest('hex') : '', ...Object.values(identities).map(identity => identity.token), env.FAAS_TOKEN, env.GREGALE_DATA_API_ACCESS_TOKEN].filter(Boolean)
  return { identities, scenarios, refreshToken, secrets }
}

export async function runHostedReplay(options, env = process.env, { signal, fetcher = fetch, emit = event => process.stdout.write(JSON.stringify(event) + '\n') } = {}) {
  signal?.throwIfAborted()
  const prepared = await prepareHostedReplay(options, env)
  signal?.throwIfAborted()
  if (options.validate_only) return { status: 'validated' }
  const url = new URL(options.url)
  if (url.protocol !== 'https:' || !url.hostname || url.username || url.password || url.search || url.hash || url.pathname !== '/') throw new Error('Use the hosted app HTTPS origin without credentials, query or fragment.')
  const report = event => {
    const redact = value => typeof value === 'string' ? prepared.secrets.reduce((text, secret) => text.split(secret).join('[redacted]'), value) : Array.isArray(value) ? value.map(redact) : value && typeof value === 'object' ? Object.fromEntries(Object.entries(value).map(([key, item]) => [key, redact(item)])) : value
    emit(redact(event))
  }
  let total = 0, failed = 0
  async function authProbe(name, token, identity = false) {
    let result = { event: 'replay_result', scenario: 'authentication', name, passed: false, expected_status: identity ? 200 : 401 }
    try {
      const response = await fetcher(new URL(identity ? '/__gregale/schema' : '/openapi.json', url), { headers: { ...(token ? { Authorization: `Bearer ${token}` } : {}), ...(identity && prepared.refreshToken ? { 'X-Gregale-Refresh-Token': prepared.refreshToken } : {}) }, redirect: 'error', signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(30000)]) : AbortSignal.timeout(30000) })
      result.status = response.status
      if (identity && response.status === 200) {
        const reader = response.body?.getReader(); const chunks = []; let size = 0
        try {
          if (reader) for (;;) {
            const { done, value } = await reader.read(); if (done) break
            size += value.length; if (size > 8192) throw new Error('Invalid serving schema')
            chunks.push(value)
          }
        } finally { if (reader) { await reader.cancel().catch(() => {}); reader.releaseLock() } }
        const contract = JSON.parse(Buffer.concat(chunks).toString('utf8'))
        result.passed = contract.version === 1 && contract.ready === true && fingerprint.test(contract.fingerprint ?? '') && (!options.fingerprint || contract.fingerprint === options.fingerprint) && (!options.require_paused || (contract.refresh_protocol === 1 && contract.refresh_paused === true && contract.refresh_verified === true && fingerprint.test(contract.refresh_fingerprint ?? '')))
      } else { await response.body?.cancel(); result.passed = !identity && response.status === 401 }
    } catch { signal?.throwIfAborted(); result.error = 'Authentication or serving contract check failed.' }
    total++; if (!result.passed) failed++; report(result)
    return result.passed
  }
  // Never run writes if authentication or any selected identity is unavailable.
  const anonymousDenied = await authProbe('Unauthenticated access denied')
  const invalidDenied = await authProbe('Invalid JWT denied', 'invalid')
  let identitiesAccepted = true
  for (const name of new Set(prepared.scenarios.flatMap(scenario => scenario.requests.map(request => request.identity)))) {
    if (!await authProbe(`Identity ${name} accepted`, prepared.identities[name].token, true)) identitiesAccepted = false
  }
  if (anonymousDenied && invalidDenied && identitiesAccepted) for (const scenario of prepared.scenarios) {
    const variables = new Map(); let scenarioFailed = 0
    for (const request of scenario.requests) {
      signal?.throwIfAborted()
      const pinned = options.fingerprint ? { ...request, expectedSchemaFingerprint: options.fingerprint } : request
      const result = await replayRequest(pinned, { url, identities: prepared.identities, refreshToken: prepared.refreshToken, signal, variables, fetcher, rejectContractMismatch: Boolean(options.fingerprint) })
      total++; if (!result.passed) { failed++; scenarioFailed++ }
      report({ event: 'replay_result', scenario: scenario.name, ...result })
    }
    report({ event: 'replay_scenario_summary', scenario: scenario.name, total: scenario.requests.length, passed: scenario.requests.length - scenarioFailed, failed: scenarioFailed })
  }
  const summary = { event: 'replay_summary', total, passed: total - failed, failed }
  report(summary)
  if (failed) throw new Error('Hosted authorization verification failed; review redacted assertion results.')
  return summary
}

async function main() {
  const options = JSON.parse(await readFile(process.argv[2], 'utf8'))
  const controller = new AbortController()
  for (const signal of ['SIGINT', 'SIGTERM']) process.once(signal, () => controller.abort())
  await runHostedReplay(options, process.env, { signal: controller.signal, emit(event) {
    if (options.json) process.stdout.write(JSON.stringify(event) + '\n')
    else if (event.event === 'replay_result') process.stdout.write(`${event.passed ? 'PASS' : 'FAIL'} ${event.scenario}/${event.name}`.replace(/[\u0000-\u001f\u007f-\u009f]/g, ' ') + '\n')
    else if (event.event === 'replay_summary') process.stdout.write(`${event.passed}/${event.total} checks passed.\n`)
  } })
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) main().catch(() => { process.stderr.write('Hosted replay failed; check collection/scenario selection, identity JWT environments, app HTTPS access and assertion results.\n'); process.exitCode = 1 })
