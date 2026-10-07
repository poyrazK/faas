import { randomBytes } from 'node:crypto'

// Mirrors pkg/api/limits.go; TestDataAPIRuntimeBounds checks this contract.
export const limits = Object.freeze({ pool: 2, rows: 1000, bodyBytes: 1048576, outputBytes: 1048576, queryMs: 15000, relations: 1000, columns: 10000, types: 20000 })

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

export function runtimeConfig(env) {
  const connection = databaseURL(env.DATABASE_URL)
  const role = decodeURIComponent(new URL(connection).username)
  const schemas = schemaNames(env.DATA_API_SCHEMAS)
  const auth = authConfig(env)
  const secret = randomBytes(32)
  return { connection, role, schemas, auth, secret, origins: (env.DATA_API_ALLOWED_ORIGINS ?? '').split(',').filter(Boolean),
    postgrestEnv: { PGRST_DB_URI: connection, PGRST_DB_SCHEMAS: schemas.join(','), PGRST_JWT_SECRET: secret.toString('base64url'),
      PGRST_SERVER_HOST: '127.0.0.1', PGRST_SERVER_PORT: '3000', PGRST_ADMIN_SERVER_PORT: '3001',
      PGRST_DB_POOL: String(limits.pool), PGRST_DB_MAX_ROWS: String(limits.rows), PGRST_DB_CHANNEL_ENABLED: 'false', PGRST_DB_PREPARED_STATEMENTS: 'false' } }
}
