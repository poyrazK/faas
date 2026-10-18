import { randomBytes } from 'node:crypto'

// Mirrors pkg/api/limits.go; TestDataAPIRuntimeBounds checks this contract.
export const limits = Object.freeze({ pool: 2, connectionReportMs: 5000, connectionReportIntervalMs: 15000, connectionReportBytes: 4096, rows: 1000, bodyBytes: 1048576, responseBytes: 16777216, outputBytes: 1048576, queryMs: 15000, lockMs: 5000, relations: 1000, columns: 10000, types: 20000, queuedRequests: 8, admissionWaitMs: 1000, overloadRetrySeconds: 1, healthProbeMs: 2000, healthBytes: 8192, rateLimitDefaultPerSecond: 10, rateLimitDefaultBurst: 20, rateLimitMaxPerSecond: 1000, rateLimitMaxBurst: 1000, rateLimitMaxSubjects: 4096, rateLimitSweepMs: 1000, userConcurrencyMaxSubjects: 4096, userConcurrencyCooldownMs: 15000, publicReadMaxRelations: 64, serverKeySubjectMaxBytes: 255 })

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

// Only SHA-256 hashes of server keys reach the runtime, in two rotation slots.
export function serverKeyConfig(env) {
  const slots = []
  for (const [slot, name] of [['a', 'DATA_API_SERVER_KEY_A_SHA256'], ['b', 'DATA_API_SERVER_KEY_B_SHA256']]) {
    const value = env[name]
    if (value === undefined) continue
    if (typeof value !== 'string' || !/^[a-f0-9]{64}$/.test(value)) throw new Error(`Invalid ${name}`)
    slots.push(Object.freeze({ slot, hash: Buffer.from(value, 'hex') }))
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
  if (typeof value !== 'string' || !/^[1-9]\d*$/.test(value)) throw new Error('Invalid DATA_API_USER_CONCURRENCY')
  return userConcurrencyPolicy(Number(value), capacity)
}

export function rateLimitPolicy(value) {
  if (value === undefined || value === null) return null
  if (!Number.isInteger(value.perSecond) || value.perSecond < 1 || value.perSecond > limits.rateLimitMaxPerSecond || !Number.isInteger(value.burst) || value.burst < 1 || value.burst > limits.rateLimitMaxBurst) throw new Error('Invalid Data API rate-limit policy')
  return Object.freeze({ perSecond: value.perSecond, burst: value.burst })
}

function rateLimitConfig(env) {
  if (env.DATA_API_RATE_LIMIT_PER_SECOND === undefined && env.DATA_API_RATE_LIMIT_BURST === undefined) return null
  const setting = (value, fallback) => {
    if (value === undefined) return fallback
    if (typeof value !== 'string' || !/^[1-9]\d*$/.test(value)) throw new Error('Invalid Data API rate-limit policy')
    return Number(value)
  }
  return rateLimitPolicy({ perSecond: setting(env.DATA_API_RATE_LIMIT_PER_SECOND, limits.rateLimitDefaultPerSecond), burst: setting(env.DATA_API_RATE_LIMIT_BURST, limits.rateLimitDefaultBurst) })
}

export function databaseURL(value) {
  const url = new URL(value)
  if (!['postgres:', 'postgresql:'].includes(url.protocol) || !url.username || !url.pathname.slice(1)) throw new Error('Invalid DATABASE_URL')
  const local = ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname)
  if (!local && url.searchParams.get('sslmode') === 'disable') throw new Error('Remote PostgreSQL requires verified TLS')
  if (!local) url.searchParams.set('sslmode', 'verify-full')
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

export function runtimeConfig(env) {
  const connection = databaseURL(env.DATABASE_URL)
  const role = decodeURIComponent(new URL(connection).username)
  const schemas = schemaNames(env.DATA_API_SCHEMAS)
  const auth = authConfig(env)
  if (env.DATA_API_COORDINATED_REFRESH !== undefined && env.DATA_API_COORDINATED_REFRESH !== '1') throw new Error('Invalid coordinated refresh setting')
  if (env.DATA_API_SQL_DEADLINE !== undefined && env.DATA_API_SQL_DEADLINE !== '1') throw new Error('Invalid SQL deadline setting')
  if (env.DATA_API_QUERY_SHAPES !== undefined && env.DATA_API_QUERY_SHAPES !== '1') throw new Error('Invalid query-shape logging setting')
  const queryShapes = env.DATA_API_QUERY_SHAPES === '1'
  const coordinatedRefresh = env.DATA_API_COORDINATED_REFRESH === '1'
  const sqlDeadline = env.DATA_API_SQL_DEADLINE === '1'
  if (env.DATA_API_AGGREGATES !== undefined && env.DATA_API_AGGREGATES !== '1') throw new Error('Invalid aggregates setting')
  const aggregates = env.DATA_API_AGGREGATES === '1'
  // HTTP aborts cannot stop an expensive aggregate; the SQL deadline can.
  if (aggregates && !sqlDeadline) throw new Error('Aggregates require DATA_API_SQL_DEADLINE=1')
  if (schemas.includes('gregale_data_api')) throw new Error('Refresh control schema cannot be exposed')
  const secret = randomBytes(32)
  return { connection, role, schemas, auth, secret, coordinatedRefresh, sqlDeadline, queryShapes, aggregates, publicRead: publicReadConfig(env.DATA_API_PUBLIC_READ, schemas), serverKeys: serverKeyConfig(env), connectionObservation: connectionObservationConfig(env), rateLimit: rateLimitConfig(env), userConcurrency: userConcurrencyConfig(env, coordinatedRefresh ? limits.pool - 1 : limits.pool), acceptedContracts: acceptedContracts(env.DATA_API_ACCEPTED_CONTRACTS), origins: (env.DATA_API_ALLOWED_ORIGINS ?? '').split(',').filter(Boolean),
    postgrestEnv: { PGRST_DB_URI: connection, PGRST_DB_SCHEMAS: schemas.join(','), PGRST_JWT_SECRET: secret.toString('base64url'),
      PGRST_SERVER_HOST: '127.0.0.1', PGRST_SERVER_PORT: '3000', PGRST_ADMIN_SERVER_PORT: '3001', PGRST_SERVER_TIMING_ENABLED: 'true',
      PGRST_DB_POOL: String(coordinatedRefresh ? limits.pool - 1 : limits.pool), PGRST_DB_MAX_ROWS: String(limits.rows), ...(sqlDeadline ? { PGRST_DB_PRE_REQUEST: 'gregale_data_api.check_deadline_request', PGRST_DB_CONFIG: 'false' } : coordinatedRefresh ? { PGRST_DB_PRE_REQUEST: 'gregale_data_api.check_request' } : {}), PGRST_DB_CHANNEL_ENABLED: 'false', PGRST_DB_PREPARED_STATEMENTS: 'false', ...(aggregates ? { PGRST_DB_AGGREGATES_ENABLED: 'true' } : {}) } }
}
