import { randomBytes } from 'node:crypto'

// Mirrors pkg/api/limits.go; TestDataAPIRuntimeBounds checks this contract.
export const limits = Object.freeze({ pool: 2, rows: 1000, bodyBytes: 1048576, responseBytes: 16777216, outputBytes: 1048576, queryMs: 15000, relations: 1000, columns: 10000, types: 20000, queuedRequests: 8, admissionWaitMs: 1000, overloadRetrySeconds: 1, healthProbeMs: 2000, healthBytes: 8192, rateLimitDefaultPerSecond: 10, rateLimitDefaultBurst: 20, rateLimitMaxPerSecond: 1000, rateLimitMaxBurst: 1000, rateLimitMaxSubjects: 4096, rateLimitSweepMs: 1000 })

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

export function runtimeConfig(env) {
  const connection = databaseURL(env.DATABASE_URL)
  const role = decodeURIComponent(new URL(connection).username)
  const schemas = schemaNames(env.DATA_API_SCHEMAS)
  const auth = authConfig(env)
  if (env.DATA_API_COORDINATED_REFRESH !== undefined && env.DATA_API_COORDINATED_REFRESH !== '1') throw new Error('Invalid coordinated refresh setting')
  if (env.DATA_API_QUERY_SHAPES !== undefined && env.DATA_API_QUERY_SHAPES !== '1') throw new Error('Invalid query-shape logging setting')
  const queryShapes = env.DATA_API_QUERY_SHAPES === '1'
  if (env.DATA_API_READ_ONLY !== undefined && !['true', 'false'].includes(env.DATA_API_READ_ONLY)) throw new Error('Invalid read-only setting')
  const readOnly = env.DATA_API_READ_ONLY === 'true'
  const coordinatedRefresh = env.DATA_API_COORDINATED_REFRESH === '1'
  if (schemas.includes('gregale_data_api')) throw new Error('Refresh control schema cannot be exposed')
  const secret = randomBytes(32)
  return { connection, role, schemas, auth, secret, coordinatedRefresh, queryShapes, readOnly, rateLimit: rateLimitConfig(env), acceptedContracts: acceptedContracts(env.DATA_API_ACCEPTED_CONTRACTS), origins: (env.DATA_API_ALLOWED_ORIGINS ?? '').split(',').filter(Boolean),
    postgrestEnv: { PGRST_DB_URI: connection, PGRST_DB_SCHEMAS: schemas.join(','), PGRST_JWT_SECRET: secret.toString('base64url'),
      PGRST_SERVER_HOST: '127.0.0.1', PGRST_SERVER_PORT: '3000', PGRST_ADMIN_SERVER_PORT: '3001',
      PGRST_DB_POOL: String(coordinatedRefresh ? limits.pool - 1 : limits.pool), PGRST_DB_MAX_ROWS: String(limits.rows), ...(coordinatedRefresh ? { PGRST_DB_PRE_REQUEST: 'gregale_data_api.check_request' } : {}), PGRST_DB_CHANNEL_ENABLED: 'false', PGRST_DB_PREPARED_STATEMENTS: 'false' } }
}
