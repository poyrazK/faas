import { open, stat, realpath } from 'node:fs/promises'
import { constants } from 'node:fs'
import { resolve, relative, isAbsolute, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { requestStore } from './dev-requests.mjs'
import { collectionScenarios, replayRequest } from './request-replay.mjs'

const object = value => value && typeof value === 'object' && !Array.isArray(value)
const fields = (value, allowed) => object(value) && Object.keys(value).every(key => allowed.includes(key))
const identifier = /^[A-Za-z][A-Za-z0-9_-]{0,63}$/
const reserved = new Set(['sub', 'iss', 'aud', 'exp', 'iat', 'nbf', 'jti', 'role', 'gregale_read_only', '__proto__', 'constructor', 'prototype'])

export async function readLocalFile(path, limit = 65536) {
  const file = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK)
  try {
    const info = await file.stat()
    if (!info.isFile() || info.size > limit) throw new Error('Use a bounded regular file.')
    const buffer = Buffer.alloc(limit + 1)
    let length = 0
    while (length < buffer.length) {
      const { bytesRead } = await file.read(buffer, length, buffer.length - length, length)
      if (!bytesRead) break
      length += bytesRead
    }
    if (length > limit) throw new Error('File exceeds its size limit.')
    return buffer.subarray(0, length).toString('utf8')
  } finally { await file.close() }
}

async function projectPath(base, value, directory = false) {
  if (typeof value !== 'string' || !value || isAbsolute(value)) throw new Error('Use project-relative paths.')
  const path = resolve(base, value), actual = await realpath(path)
  if ((relative(base, actual) === '..' || relative(base, actual).startsWith('..' + sep)) || isAbsolute(relative(base, actual))) throw new Error('Project paths must remain inside the project.')
  if (directory && !(await stat(actual)).isDirectory()) throw new Error('Migration and check paths must be directories.')
  return path
}

export async function loadDevProject(options) {
  const base = await realpath(options.directory)
  const configPath = options.config || resolve(base, 'data-api.dev.json')
  let config, legacy = false
  try { config = JSON.parse(await readLocalFile(configPath)) }
  catch (error) {
    if (options.config || error.code !== 'ENOENT') throw new Error('Cannot read development config; use a regular JSON file under 64 KiB.')
    // Existing notes starters retain their original required verification.
    legacy = true
    config = { version: 1, identities: { alice: { subject: 'alice' }, bob: { subject: 'bob' } } }
  }
  if (!fields(config, ['version', 'migrations', 'output', 'identities', 'verify', 'seed', 'check', 'public_read', 'public_rpc']) || config.version !== 1) throw new Error('Use a version 1 development config with known fields.')
  if (!object(config.identities) || Object.keys(config.identities).length < 2 || Object.keys(config.identities).length > 100) throw new Error('Configure two to 100 local test identities.')
  const identities = Object.fromEntries(Object.entries(config.identities).map(([name, value]) => {
    if (!identifier.test(name) || reserved.has(name) || !fields(value, ['subject', 'claims', 'read_only']) || (value.read_only !== undefined && typeof value.read_only !== 'boolean') || typeof value.subject !== 'string' || !value.subject || value.subject.length > 256 || (value.claims !== undefined && (!object(value.claims) || Object.keys(value.claims).some(key => reserved.has(key))))) throw new Error('Use named identities with subjects and non-reserved custom claims.')
    return [name, { subject: value.subject, claims: value.claims ?? {}, ...(value.read_only?{read_only:true}:{}) }]
  }))
  if (new Set(Object.values(identities).map(value => value.subject)).size < 2) throw new Error('Verification needs at least two distinct subjects.')
  const migrations = await projectPath(base, config.migrations ?? 'migrations/sql', true)
  let verification
  if (legacy) verification = { collection: fileURLToPath(new URL('./starter-verification.json', import.meta.url)), scenarios: ['notes-rls'] }
  else {
    if (!fields(config.verify, ['collection', 'scenarios']) || !Array.isArray(config.verify.scenarios) || !config.verify.scenarios.length || new Set(config.verify.scenarios).size !== config.verify.scenarios.length || config.verify.scenarios.some(name => typeof name !== 'string' || !identifier.test(name))) throw new Error('Configure required verification scenarios.')
    verification = { collection: await projectPath(base, config.verify.collection), scenarios: config.verify.scenarios }
  }
  const output = options.output_set || !config.output ? options.output : resolve(base, config.output)
  if (config.output !== undefined && (typeof config.output !== 'string' || !config.output || isAbsolute(config.output) || (relative(base, resolve(base, config.output)) === '..' || relative(base, resolve(base, config.output)).startsWith('..' + sep)))) throw new Error('Use a project-relative types output.')
  const seed = config.seed === undefined ? null : await projectPath(base, config.seed)
  const seedSQL = seed ? await readLocalFile(seed, 1048576) : null
  const check = config.check ?? { directory: 'client', commands: [['npm', 'run', 'typecheck'], ['npm', 'test']] }
  if (!fields(check, ['directory', 'commands']) || typeof check.directory !== 'string' || !check.directory || !Array.isArray(check.commands) || !check.commands.length || check.commands.length > 10 || check.commands.some(command => !Array.isArray(command) || !command.length || command.length > 100 || command.some(arg => typeof arg !== 'string' || !arg || arg.includes('\0')))) throw new Error('Use explicit argument arrays for client checks.')
  const checkDirectory = options.check ? await projectPath(base, check.directory, true) : resolve(base, check.directory)
  const protectedPaths = [configPath, verification.collection, seed, options.baseline || resolve(base, '.gregale/data-api-contract.json'), options.replay, options.fixture, options.data_check].filter(Boolean)
  let outputInfo
  try { outputInfo = await stat(output) } catch (error) { if (error.code !== 'ENOENT') throw error }
  for (const path of protectedPaths) {
    let info
    try { info = await stat(path) } catch (error) { if (error.code !== 'ENOENT') throw error }
    if (resolve(output) === resolve(path) || (outputInfo && info && outputInfo.dev === info.dev && outputInfo.ino === info.ino)) throw new Error('Types output must not replace configuration, seeds, requests or the baseline.')
  }
  // Anonymous reads use the hosted allowlist format; runtimeConfig validates entries.
  if (config.public_read !== undefined && (!Array.isArray(config.public_read) || !config.public_read.length || config.public_read.some(name => typeof name !== 'string'))) throw new Error('Use public_read as a list of schema.relation names.')
  if (config.public_rpc !== undefined && (!Array.isArray(config.public_rpc) || !config.public_rpc.length || config.public_rpc.some(name => typeof name !== 'string'))) throw new Error('Use public_rpc as a list of schema.function names.')
  const project = { base, migrations, output, identities, verification, seedSQL, check: { directory: checkDirectory, commands: check.commands }, legacy, ...(config.public_read ? { publicRead: config.public_read.join(',') } : {}), ...(config.public_rpc ? { publicRpc: config.public_rpc.join(',') } : {}) }
  await verificationScenarios(project)
  return project
}

export async function verificationScenarios(project) {
  const collection = await requestStore(project.verification.collection, { identities: Object.keys(project.identities) }).read(true)
  const scenarios = project.verification.scenarios.flatMap(name => collectionScenarios(collection, name))
  const requests = scenarios.flatMap(scenario => scenario.requests)
  if (!requests.length || new Set(requests.map(request => project.identities[request.identity].subject)).size < 2) throw new Error('Required verification must exercise at least two distinct subjects.')
  return scenarios
}

export async function verifyProject(url, identities, project, signal) {
  if ((await fetch(`${url}/openapi.json`, { signal })).status !== 401) throw new Error('Unauthenticated access was accepted.')
  if ((await fetch(`${url}/openapi.json`, { headers: { Authorization: 'Bearer invalid' }, signal })).status !== 401) throw new Error('Invalid authentication was accepted.')
  for (const scenario of await verificationScenarios(project)) {
    const variables = new Map()
    for (const request of scenario.requests) {
      const result = await replayRequest(request, { url, identities, signal, variables })
      if (!result.passed) throw new Error(`Required verification failed: ${scenario.name}/${request.name}.`)
    }
  }
}
