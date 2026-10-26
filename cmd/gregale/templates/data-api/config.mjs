import { createHash, randomBytes } from 'node:crypto'
import { mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { rootCertificates } from 'node:tls'

// Mirrors pkg/api/limits.go; TestDataAPIRuntimeBounds checks this contract.
export const limits = Object.freeze({ pool: 2, poolMax: 8, connectionReportMs: 5000, connectionReportIntervalMs: 15000, connectionReportBytes: 4096, rows: 1000, bodyBytes: 1048576, responseBytes: 16777216, outputBytes: 1048576, queryMs: 15000, lockMs: 5000, relations: 1000, columns: 10000, types: 20000, queuedRequests: 8, admissionWaitMs: 1000, overloadRetrySeconds: 1, healthProbeMs: 2000, healthBytes: 8192, rateLimitDefaultPerSecond: 10, rateLimitDefaultBurst: 20, rateLimitMaxPerSecond: 1000, rateLimitMaxBurst: 1000, rateLimitMaxSubjects: 4096, rateLimitSweepMs: 1000, userConcurrencyMaxSubjects: 4096, userConcurrencyCooldownMs: 15000, publicReadMaxRelations: 64, serverKeySubjectMaxBytes: 255, publicReadMaxAgeSeconds: 3600, cacheTagMaxBytes: 128, cacheTagsMax: 32, serverKeyPermissionsBytes: 4096, serverKeyPermissionsEntries: 64, publicRpcMaxFunctions: 64, usageReportMs: 5000, usageReportIntervalMs: 15000, usageReportMaxHours: 48, usageMonthlyCapMax: 1000000000000, usageRestoreJumpMs: 5000, storageObjectDefaultBytes: 26214400, storageObjectMaxBytes: 104857600, storageTransfers: 4, storageTransferMs: 120000, storageSignSeconds: 60 })

export const relationPart = /^[A-Za-z_][A-Za-z0-9_]{0,62}$/
const schemaPart = /^[a-z][a-z0-9_]{0,62}$/

// Owner allowlist of schema-qualified relations readable without a token.
export function publicReadConfig(value, schemas) {
  if (value === undefined) return null
  const invalid = () => { throw new Error('Invalid DATA_API_PUBLIC_READ') }
  if (typeof value !== 'string' || !value) return invalid()
  const names = value.split(',')
  if (names.length > limits.publicReadMaxRelations || new Set(names).size !== names.length) return invalid()
  const relations = names.map(name => {
    const [schema, relation, ...extra] = name.split('.')
    if (extra.length || !schemaPart.test(schema ?? '') || !relationPart.test(relation ?? '') || !schemas.includes(schema)) return invalid()
    return Object.freeze({ schema, relation })
  })
  return Object.freeze(relations)
}

// Edge freshness for canonical anonymous reads and RPC calls; the gateway cache
// rule stores them.
export function publicReadMaxAge(value, anonymous) {
  if (value === undefined) return null
  if (typeof value !== 'string' || !/^[1-9]\d*$/.test(value) || Number(value) > limits.publicReadMaxAgeSeconds) throw new Error('Invalid DATA_API_PUBLIC_READ_MAX_AGE')
  if (!anonymous) throw new Error('DATA_API_PUBLIC_READ_MAX_AGE requires DATA_API_PUBLIC_READ or DATA_API_PUBLIC_RPC')
  return Number(value)
}

const functionPart = /^[a-z][a-z0-9_]{0,62}$/

// Owner allowlist of approved functions callable without a token, by GET only.
// The runtime rechecks approval and volatility at startup.
export function publicRpcConfig(value, schemas) {
  if (value === undefined) return null
  const invalid = () => { throw new Error('Invalid DATA_API_PUBLIC_RPC') }
  if (typeof value !== 'string' || !value) return invalid()
  const names = value.split(',')
  if (names.length > limits.publicRpcMaxFunctions || new Set(names).size !== names.length) return invalid()
  return Object.freeze(names.map(name => {
    const [schema, fn, ...extra] = name.split('.')
    if (extra.length || !schemaPart.test(schema ?? '') || !functionPart.test(fn ?? '') || !schemas.includes(schema)) return invalid()
    return Object.freeze({ schema, name: fn })
  }))
}
const permissionFields = ['version', 'access', 'relations', 'rpcs', 'subjects']

// Optional per-slot restrictions. Absent permissions keep full server access.
// Entries outside the exposed schemas never match, so they only ever deny.
export function serverKeyPermissions(value, name) {
  if (value === undefined) return null
  const invalid = () => { throw new Error(`Invalid ${name}`) }
  if (typeof value !== 'string' || Buffer.byteLength(value) > limits.serverKeyPermissionsBytes) return invalid()
  let permissions
  try { permissions = JSON.parse(value) } catch { return invalid() }
  if (!permissions || typeof permissions !== 'object' || Array.isArray(permissions) || Object.keys(permissions).some(key => !permissionFields.includes(key)) || permissions.version !== 1 ||
    (permissions.access !== undefined && !['read', 'write'].includes(permissions.access)) || (permissions.subjects !== undefined && typeof permissions.subjects !== 'boolean')) return invalid()
  const entries = (list, pattern) => {
    if (list === undefined) return null
    if (!Array.isArray(list) || !list.length || list.length > limits.serverKeyPermissionsEntries || new Set(list).size !== list.length) return invalid()
    return Object.freeze(list.map(entry => {
      const [schema, object, ...extra] = typeof entry === 'string' ? entry.split('.') : []
      if (extra.length || !schemaPart.test(schema ?? '') || !pattern.test(object ?? '')) return invalid()
      return Object.freeze({ schema, name: object })
    }))
  }
  return Object.freeze({ access: permissions.access ?? 'write', relations: entries(permissions.relations, relationPart), rpcs: entries(permissions.rpcs, functionPart), subjects: permissions.subjects ?? true })
}

// Only SHA-256 hashes of server keys reach the runtime, in two rotation slots.
export function serverKeyConfig(env) {
  const slots = []
  for (const [slot, name] of [['a', 'DATA_API_SERVER_KEY_A_SHA256'], ['b', 'DATA_API_SERVER_KEY_B_SHA256']]) {
    const value = env[name]
    if (value === undefined) continue
    if (typeof value !== 'string' || !/^[a-f0-9]{64}$/.test(value)) throw new Error(`Invalid ${name}`)
    const permissionsName = `DATA_API_SERVER_KEY_${slot.toUpperCase()}_PERMISSIONS`
    slots.push(Object.freeze({ slot, hash: Buffer.from(value, 'hex'), permissions: serverKeyPermissions(env[permissionsName], permissionsName) }))
  }
  if (slots.length === 2 && slots[0].hash.equals(slots[1].hash)) throw new Error('Server key slots must differ')
  return slots.length ? Object.freeze(slots) : null
}

export function userConcurrencyPolicy(value, capacity = limits.pool) {
  if (value === undefined || value === null) return null
  if (!Number.isInteger(value) || value < 1 || value > capacity) throw new Error('Invalid Data API user concurrency policy')
  return value
}

function userConcurrencyConfig(env, capacity) {
  const value = env.DATA_API_USER_CONCURRENCY
  if (value === undefined) return null
  if (typeof value !== 'string' || !/^[1-9]\d*$/.test(value) || Number(value) > limits.poolMax) throw new Error('Invalid DATA_API_USER_CONCURRENCY')
  // A value above the engine pool acts as the pool (ADR-20261021180534), so
  // changing the pool alone never stops the runtime from starting.
  return userConcurrencyPolicy(Math.min(Number(value), capacity), capacity)
}

export function rateLimitPolicy(value) {
  if (value === undefined || value === null) return null
  if (!Number.isInteger(value.perSecond) || value.perSecond < 1 || value.perSecond > limits.rateLimitMaxPerSecond || !Number.isInteger(value.burst) || value.burst < 1 || value.burst > limits.rateLimitMaxBurst) throw new Error('Invalid Data API rate-limit policy')
  return Object.freeze({ perSecond: value.perSecond, burst: value.burst })
}

function rateLimitConfig(env, prefix = 'DATA_API_RATE_LIMIT') {
  const perSecond = env[`${prefix}_PER_SECOND`], burst = env[`${prefix}_BURST`]
  if (perSecond === undefined && burst === undefined) return null
  const setting = (value, fallback) => {
    if (value === undefined) return fallback
    if (typeof value !== 'string' || !/^[1-9]\d*$/.test(value)) throw new Error('Invalid Data API rate-limit policy')
    return Number(value)
  }
  return rateLimitPolicy({ perSecond: setting(perSecond, limits.rateLimitDefaultPerSecond), burst: setting(burst, limits.rateLimitDefaultBurst) })
}

// The engine pool (ADR-20261021180534): DATA_API_POOL from 1 to poolMax,
// default 2. Coordinated refresh keeps one connection for its reader.
export function poolConfig(value, coordinatedRefresh) {
  if (value === undefined) return limits.pool
  const pool = Number(value)
  if (typeof value !== 'string' || !/^[1-9]\d*$/.test(value) || pool > limits.poolMax) throw new Error('Invalid DATA_API_POOL')
  if (coordinatedRefresh && pool < 2) throw new Error('Coordinated refresh needs DATA_API_POOL of at least 2')
  return pool
}

// enginePool is the engine's share of the pool: all of it, less the refresh
// reader's connection under coordinated refresh.
export function enginePool(config) {
  return config.engine ?? (config.coordinatedRefresh ? limits.pool - 1 : limits.pool)
}

// Server-key requests together hold at most this many admission slots:
// by default all but one, so users and visitors always keep a slot. A limit
// at or above the engine pool keeps nothing back, so it is null and only
// admission bounds server keys; changing the pool alone never stops the
// runtime from starting.
export function serverKeyConcurrencyConfig(value, engine) {
  const limit = value === undefined ? engine - 1 : Number(value)
  if (value !== undefined && (typeof value !== 'string' || !/^[1-9]\d*$/.test(value) || limit > limits.poolMax)) throw new Error('Invalid DATA_API_SERVER_KEY_CONCURRENCY')
  return limit >= 1 && limit < engine ? limit : null
}

export function databaseURL(value) {
  const url = new URL(value)
  if (!['postgres:', 'postgresql:'].includes(url.protocol) || !url.username || !url.pathname.slice(1)) throw new Error('Invalid DATABASE_URL')
  const local = ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname)
  if (!local && url.searchParams.get('sslmode') === 'disable') throw new Error('Remote PostgreSQL requires verified TLS')
  if (!local) url.searchParams.set('sslmode', 'verify-full')
  return url.toString()
}

// libpq has no built-in trust store: verify-full without sslrootcert refuses
// to connect. PostgREST therefore gets the roots Node verifies the same server
// with, the bundled store plus NODE_EXTRA_CA_CERTS.
const rootFiles = new Map()
export function engineDatabaseURL(connection, env) {
  const url = new URL(connection)
  if (url.searchParams.get('sslmode') !== 'verify-full' || url.searchParams.has('sslrootcert')) return connection
  let extra = ''
  // Node also starts without an unreadable NODE_EXTRA_CA_CERTS file.
  try { extra = env.NODE_EXTRA_CA_CERTS ? readFileSync(env.NODE_EXTRA_CA_CERTS, 'utf8') : '' } catch {}
  const bundle = [...rootCertificates, extra].filter(Boolean).join('\n') + '\n'
  const key = createHash('sha256').update(bundle).digest('hex')
  if (!rootFiles.has(key)) {
    const file = join(mkdtempSync(join(tmpdir(), 'gregale-data-api-')), 'roots.pem')
    writeFileSync(file, bundle, { mode: 0o600, flag: 'wx' })
    rootFiles.set(key, file)
  }
  url.searchParams.set('sslrootcert', rootFiles.get(key))
  return url.toString()
}

export function schemaNames(value = 'api') {
  const names = value.split(',')
  if (names.length > 8 || names.some(n => !/^[a-z][a-z0-9_]{0,62}$/.test(n) || n.startsWith('pg_') || n === 'information_schema') || new Set(names).size !== names.length) throw new Error('Invalid DATA_API_SCHEMAS')
  return names
}

export function authConfig(env) {
  const issuer = new URL(env.DATA_API_ISSUER)
  const jwks = new URL(env.DATA_API_JWKS_URL)
  if (issuer.protocol !== 'https:' || jwks.protocol !== 'https:' || issuer.username || issuer.password || jwks.username || jwks.password || !env.DATA_API_AUDIENCE) throw new Error('HTTPS issuer, JWKS URL and audience are required')
  return { issuer: env.DATA_API_ISSUER, jwks, audience: env.DATA_API_AUDIENCE }
}

// Owner deployment policy; it is bound to one immutable serving contract.
export function acceptedContracts(value) {
  if (value === undefined) return undefined
  const invalid = () => { throw new Error('Invalid DATA_API_ACCEPTED_CONTRACTS') }
  if (typeof value !== 'string' || Buffer.byteLength(value) > limits.outputBytes) return invalid()
  let policy
  try { policy = JSON.parse(value) } catch { return invalid() }
  const hash = value => typeof value === 'string' && /^[a-f0-9]{64}$/.test(value)
  if (!policy || Array.isArray(policy) || Object.keys(policy).sort().join(',') !== 'accepted_fingerprints,current_fingerprint,version' || policy.version !== 1 || !hash(policy.current_fingerprint) || !Array.isArray(policy.accepted_fingerprints) || !policy.accepted_fingerprints.length || policy.accepted_fingerprints.some(value => !hash(value) || value === policy.current_fingerprint) || new Set(policy.accepted_fingerprints).size !== policy.accepted_fingerprints.length) return invalid()
  return Object.freeze({ ...policy, accepted_fingerprints: Object.freeze([...policy.accepted_fingerprints]) })
}

export function servingContracts(config) {
  // Revalidate direct callers too, then copy rather than retaining mutable config.
  const policy = config.acceptedContracts === undefined ? undefined : acceptedContracts(JSON.stringify(config.acceptedContracts))
  if (policy && policy.current_fingerprint !== config.fingerprint) throw new Error('Accepted contracts do not match the serving contract')
  return new Set(policy?.accepted_fingerprints ?? [])
}

// Owner opt-in; generation values are immutable process configuration. An old
// snapshot must not learn a new generation merely by asking the control plane.
export function connectionObservationConfig(env) {
  if (env.DATA_API_CONNECTION_OBSERVATION === undefined) return undefined
  const invalid = () => { throw new Error('Invalid Data API connection observation configuration') }
  if (typeof env.DATA_API_CONNECTION_OBSERVATION !== 'string' || Buffer.byteLength(env.DATA_API_CONNECTION_OBSERVATION) > limits.connectionReportBytes) return invalid()
  let value, api, identity
  try {
    value = JSON.parse(env.DATA_API_CONNECTION_OBSERVATION)
    api = new URL(value.api_url)
    identity = new URL(env.FAAS_WORKLOAD_IDENTITY_ENDPOINT)
  } catch { return invalid() }
  if (Object.keys(value).sort().join(',') !== 'api_url,binding_id,credential_generation,database_generation,database_identity' ||
    !/^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/.test(value.binding_id) ||
    !/^[a-f0-9]{64}$/.test(value.database_identity) ||
    !Number.isSafeInteger(value.database_generation) || value.database_generation < 1 ||
    !Number.isSafeInteger(value.credential_generation) || value.credential_generation < 1 ||
    api.protocol !== 'https:' || api.username || api.password || api.pathname !== '/' || api.search || api.hash ||
    identity.protocol !== 'http:' || !['127.0.0.1', '[::1]'].includes(identity.hostname) || identity.username || identity.password || identity.pathname !== '/oidc/token' || identity.search || identity.hash) return invalid()
  identity.searchParams.set('audience', 'gregale:data-api:connections')
  return Object.freeze({ ...value, endpoint: new URL('/v1/runtime/data-api/connection-observations', api).href, identity: identity.href })
}

// Owner usage reporting and monthly caps (ADR 20261019102108).
const capFields = ['requests', 'anonymous_requests']

// Optional owner caps per UTC calendar month. Absent: no caps.
export function monthlyCaps(value) {
  if (value === undefined) return null
  const invalid = () => { throw new Error('Invalid DATA_API_MONTHLY_CAPS') }
  if (typeof value !== 'string' || value.length > 256) return invalid()
  let caps
  try { caps = JSON.parse(value) } catch { return invalid() }
  if (!caps || typeof caps !== 'object' || Array.isArray(caps) || caps.version !== 1 || Object.keys(caps).some(key => key !== 'version' && !capFields.includes(key))) return invalid()
  const cap = name => {
    if (caps[name] === undefined) return null
    if (!Number.isSafeInteger(caps[name]) || caps[name] < 1 || caps[name] > limits.usageMonthlyCapMax) return invalid()
    return caps[name]
  }
  const parsed = { requests: cap('requests'), anonymous_requests: cap('anonymous_requests') }
  if (parsed.requests === null && parsed.anonymous_requests === null) return invalid()
  return Object.freeze(parsed)
}

// Reporting is on when the app names the platform API; caps need it, since
// only the platform sees every instance's traffic.
export function usageConfig(env) {
  if (env.DATA_API_USAGE_URL === undefined) {
    if (env.DATA_API_MONTHLY_CAPS !== undefined) throw new Error('DATA_API_MONTHLY_CAPS requires DATA_API_USAGE_URL')
    return null
  }
  const invalid = () => { throw new Error('Invalid Data API usage reporting configuration') }
  let api, identity
  try {
    api = new URL(env.DATA_API_USAGE_URL)
    identity = new URL(env.FAAS_WORKLOAD_IDENTITY_ENDPOINT)
  } catch { return invalid() }
  if (api.protocol !== 'https:' || api.username || api.password || api.pathname !== '/' || api.search || api.hash ||
    identity.protocol !== 'http:' || !['127.0.0.1', '[::1]'].includes(identity.hostname) || identity.username || identity.password || identity.pathname !== '/oidc/token' || identity.search || identity.hash) return invalid()
  identity.searchParams.set('audience', 'gregale:data-api:usage')
  return Object.freeze({ endpoint: new URL('/v1/runtime/data-api/usage', api).href, identity: identity.href, caps: monthlyCaps(env.DATA_API_MONTHLY_CAPS) })
}

// File storage (ADR-20261021101510): a bucket binding's secret suffixes.
const suffixes = ['ENDPOINT', 'REGION', 'BUCKET', 'ACCESS_KEY_ID', 'SECRET_ACCESS_KEY']

// storageBindings maps each bucket bound to the app (GREGALE_S3_<NAME>_*
// secrets) to its credentials, named by the binding in lower case.
export function storageBindings(env) {
  const bindings = new Map()
  for (const key of Object.keys(env)) {
    const match = /^GREGALE_S3_([A-Z0-9_]+)_ENDPOINT$/.exec(key)
    if (!match) continue
    const prefix = `GREGALE_S3_${match[1]}`
    const name = match[1].toLowerCase()
    if (!/^[a-z][a-z0-9_]{0,62}$/.test(name) || suffixes.some(s => !env[`${prefix}_${s}`])) continue
    let endpoint
    try { endpoint = new URL(env[`${prefix}_ENDPOINT`]) } catch { continue }
    if (endpoint.protocol !== 'https:' && !['localhost', '127.0.0.1'].includes(endpoint.hostname)) continue
    bindings.set(name, Object.freeze({
      endpoint, region: env[`${prefix}_REGION`], bucket: env[`${prefix}_BUCKET`],
      accessKeyId: env[`${prefix}_ACCESS_KEY_ID`], secretAccessKey: env[`${prefix}_SECRET_ACCESS_KEY`],
      virtualHosted: env[`${prefix}_ADDRESSING_STYLE`] === 'virtual',
    }))
  }
  return bindings
}

// storageConfig reads DATA_API_STORAGE=on and the per-object ceiling.
export function storageConfig(env, schemas) {
  if (env.DATA_API_STORAGE === undefined) {
    if (env.DATA_API_STORAGE_MAX_OBJECT_BYTES !== undefined) throw new Error('DATA_API_STORAGE_MAX_OBJECT_BYTES needs DATA_API_STORAGE=on')
    return null
  }
  if (env.DATA_API_STORAGE !== 'on') throw new Error('Invalid DATA_API_STORAGE')
  const buckets = storageBindings(env)
  if (!buckets.size) throw new Error('DATA_API_STORAGE needs a bucket bound to the app')
  let maxObjectBytes = limits.storageObjectDefaultBytes
  if (env.DATA_API_STORAGE_MAX_OBJECT_BYTES !== undefined) {
    maxObjectBytes = Number(env.DATA_API_STORAGE_MAX_OBJECT_BYTES)
    if (!/^[1-9][0-9]*$/.test(env.DATA_API_STORAGE_MAX_OBJECT_BYTES) || !Number.isSafeInteger(maxObjectBytes) || maxObjectBytes > limits.storageObjectMaxBytes) throw new Error('Invalid DATA_API_STORAGE_MAX_OBJECT_BYTES')
  }
  return Object.freeze({ buckets, maxObjectBytes, schema: schemas[0] })
}

export function runtimeConfig(env) {
  const connection = databaseURL(env.DATABASE_URL)
  const role = decodeURIComponent(new URL(connection).username)
  const schemas = schemaNames(env.DATA_API_SCHEMAS)
  const auth = authConfig(env)
  if (env.DATA_API_COORDINATED_REFRESH !== undefined && env.DATA_API_COORDINATED_REFRESH !== '1') throw new Error('Invalid coordinated refresh setting')
  if (env.DATA_API_SQL_DEADLINE !== undefined && env.DATA_API_SQL_DEADLINE !== '1') throw new Error('Invalid SQL deadline setting')
  if (env.DATA_API_QUERY_SHAPES !== undefined && env.DATA_API_QUERY_SHAPES !== '1') throw new Error('Invalid query-shape logging setting')
  const queryShapes = env.DATA_API_QUERY_SHAPES === '1'
  if (env.DATA_API_READ_ONLY !== undefined && !['true', 'false'].includes(env.DATA_API_READ_ONLY)) throw new Error('Invalid read-only setting')
  const readOnly = env.DATA_API_READ_ONLY === 'true'
  const coordinatedRefresh = env.DATA_API_COORDINATED_REFRESH === '1'
  const sqlDeadline = env.DATA_API_SQL_DEADLINE === '1'
  if (env.DATA_API_AGGREGATES !== undefined && env.DATA_API_AGGREGATES !== '1') throw new Error('Invalid aggregates setting')
  const aggregates = env.DATA_API_AGGREGATES === '1'
  // HTTP aborts cannot stop an expensive aggregate; the SQL deadline can.
  if (aggregates && !sqlDeadline) throw new Error('Aggregates require DATA_API_SQL_DEADLINE=1')
  if (schemas.includes('gregale_data_api')) throw new Error('Refresh control schema cannot be exposed')
  const secret = randomBytes(32)
  const storage = storageConfig(env, schemas)
  const pool = poolConfig(env.DATA_API_POOL, coordinatedRefresh)
  const engine = coordinatedRefresh ? pool - 1 : pool
  return { connection, role, schemas, auth, secret, storage, pool, engine, serverKeyConcurrency: serverKeyConcurrencyConfig(env.DATA_API_SERVER_KEY_CONCURRENCY, engine), serverKeyRateLimit: rateLimitConfig(env, 'DATA_API_SERVER_KEY_RATE_LIMIT'), coordinatedRefresh, sqlDeadline, queryShapes, readOnly, aggregates, publicRead: publicReadConfig(env.DATA_API_PUBLIC_READ, schemas), serverKeys: serverKeyConfig(env),
    publicRpc: publicRpcConfig(env.DATA_API_PUBLIC_RPC, schemas), usage: usageConfig(env), publicReadMaxAge: publicReadMaxAge(env.DATA_API_PUBLIC_READ_MAX_AGE, env.DATA_API_PUBLIC_READ !== undefined || env.DATA_API_PUBLIC_RPC !== undefined), connectionObservation: connectionObservationConfig(env), rateLimit: rateLimitConfig(env), userConcurrency: userConcurrencyConfig(env, engine), acceptedContracts: acceptedContracts(env.DATA_API_ACCEPTED_CONTRACTS), origins: (env.DATA_API_ALLOWED_ORIGINS ?? '').split(',').filter(Boolean),
    postgrestEnv: { PGRST_DB_URI: engineDatabaseURL(connection, env), PGRST_DB_SCHEMAS: schemas.join(','), PGRST_JWT_SECRET: secret.toString('base64url'),
      PGRST_SERVER_HOST: '127.0.0.1', PGRST_SERVER_PORT: '3000', PGRST_ADMIN_SERVER_PORT: '3001', PGRST_SERVER_TIMING_ENABLED: 'true',
      PGRST_DB_POOL: String(engine), PGRST_DB_MAX_ROWS: String(limits.rows), ...(sqlDeadline ? { PGRST_DB_PRE_REQUEST: 'gregale_data_api.check_deadline_request', PGRST_DB_CONFIG: 'false' } : coordinatedRefresh ? { PGRST_DB_PRE_REQUEST: 'gregale_data_api.check_request' } : {}), PGRST_DB_CHANNEL_ENABLED: 'false', PGRST_DB_PREPARED_STATEMENTS: 'false', ...(aggregates ? { PGRST_DB_AGGREGATES_ENABLED: 'true' } : {}),
      // Storage authorizes before transferring with Prefer: tx=rollback dry runs.
      ...(storage ? { PGRST_DB_TX_END: 'commit-allow-override' } : {}) } }
}
