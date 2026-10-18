import { createHash, timingSafeEqual } from 'node:crypto'
import { isIP } from 'node:net'
import pg from 'pg'
import { limits, relationPart } from './config.mjs'

// Access kinds are gateway-owned claims (ADR 20261018170742). Every kind uses the
// binding login as its SQL role; PostgreSQL policies decide which rows match.
export const accessKinds = Object.freeze(['user', 'anonymous', 'server'])

export const serverKeyShape = /^gdsk_[A-Za-z0-9_-]{43}$/

// Returns the matching slot without short-circuiting on the first comparison.
export function matchServerKey(slots, token) {
  if (!slots || typeof token !== 'string' || !serverKeyShape.test(token)) return null
  const digest = createHash('sha256').update(token).digest()
  let matched = null
  for (const entry of slots) if (timingSafeEqual(digest, entry.hash) && matched === null) matched = entry.slot
  return matched
}

// A trusted backend may act for one application subject. Values are opaque.
export function serverSubject(value) {
  if (value === undefined) return { subject: null }
  if (typeof value !== 'string' || !value || Buffer.byteLength(value) > limits.serverKeySubjectMaxBytes || /[\u0000-\u001f\u007f]/.test(value)) return { invalid: true }
  return { subject: value }
}

const aggregateFunctions = new Set(['sum', 'avg', 'min', 'max', 'count'])

// Lists every relation a PostgREST select parameter embeds. Returns null for
// syntax this conservative reader cannot classify; callers must then reject.
export function selectEmbeddings(select) {
  if (typeof select !== 'string' || select.length > 4096 || select.includes('"')) return null
  const embedded = []
  let depth = 0, start = 0
  for (let i = 0; i < select.length; i++) {
    const char = select[i]
    if (char === ',') start = i + 1
    else if (char === ')') {
      if (--depth < 0) return null
      start = i + 1
    } else if (char === '(') {
      depth++
      let token = select.slice(start, i).trim()
      start = i + 1
      // An alias precedes a single colon; casts use a double colon after ')'.
      const alias = /^([A-Za-z_][A-Za-z0-9_]*):(?!:)(.*)$/.exec(token)
      if (alias) token = alias[2]
      if (token.startsWith('...')) token = token.slice(3)
      const column = /^([A-Za-z_][A-Za-z0-9_]*)\.([a-z]+)$/.exec(token)
      if (column) {
        if (!aggregateFunctions.has(column[2]) || select[i + 1] !== ')') return null
        continue
      }
      if (token === 'count' && select[i + 1] === ')') continue
      const relation = token.split('!')[0]
      if (!relationPart.test(relation) || token.split('!').slice(1).some(hint => !relationPart.test(hint))) return null
      embedded.push(relation)
    }
  }
  return depth === 0 ? embedded : null
}

// Decides whether a request without Authorization may run anonymously. Unlisted
// targets answer like any missing token; unlisted embeddings are a policy denial.
export function publicReadTarget(req, config) {
  if (!config.publicRead || !['GET', 'HEAD'].includes(req.method)) return { code: 'token_required' }
  let url
  try { url = new URL(req.url, 'http://127.0.0.1') } catch { return { code: 'token_required' } }
  if (!url.pathname.startsWith('/rest/v1/')) return { code: 'token_required' }
  let relation
  try { relation = decodeURIComponent(url.pathname.slice('/rest/v1/'.length)) } catch { return { code: 'token_required' } }
  const schema = req.headers['accept-profile'] ?? config.schemas[0]
  const listed = name => config.publicRead.some(entry => entry.schema === schema && entry.relation === name)
  if (!relationPart.test(relation) || !listed(relation)) return { code: 'token_required' }
  const selects = url.searchParams.getAll('select')
  if (selects.length > 1) return { code: 'public_read_not_allowed' }
  if (selects.length === 1) {
    const embedded = selectEmbeddings(selects[0])
    if (!embedded || embedded.some(name => !listed(name))) return { code: 'public_read_not_allowed' }
  }
  return { schema, relation }
}

function expandIPv6(value) {
  const [head, tail = ''] = value.split('::')
  const parse = part => part ? part.split(':') : []
  const left = parse(head), right = parse(tail)
  const groups = value.includes('::') ? [...left, ...Array(8 - left.length - right.length).fill('0'), ...right] : left
  return groups.map(group => parseInt(group, 16))
}

// The edge stamps one trusted client address (x-faas-client-ip). IPv6 clients
// are grouped by /64 so address rotation inside one allocation shares a bucket.
export function clientKey(req) {
  const value = req.headers['x-faas-client-ip']
  if (typeof value !== 'string') return 'unknown'
  const mapped = /^::ffff:(\d+\.\d+\.\d+\.\d+)$/i.exec(value)
  if (mapped) return mapped[1]
  const version = isIP(value)
  if (version === 4) return value
  if (version !== 6 || value.includes('.')) return 'unknown'
  return expandIPv6(value.toLowerCase()).slice(0, 4).map(group => group.toString(16)).join(':') + '::/64'
}

const publicRelationsQuery = `SELECT t.schema, t.relation, c.relkind AS kind, c.relrowsecurity AS rls,
 COALESCE((SELECT lower(o.option_value) FROM pg_options_to_table(c.reloptions) o WHERE o.option_name='security_invoker'),'') AS invoker
 FROM unnest($1::text[],$2::text[]) AS t(schema,relation)
 LEFT JOIN pg_namespace n ON n.nspname=t.schema
 LEFT JOIN pg_class c ON c.relnamespace=n.oid AND c.relname=t.relation`

// Public reads require PostgreSQL to own row decisions for every listed relation:
// RLS-enabled tables or security-invoker views. Policies themselves stay the
// owner's responsibility.
export function unsafePublicRelations(rows) {
  const unsafe = []
  for (const row of rows) {
    const table = ['r', 'p'].includes(row.kind) && row.rls === true
    const view = row.kind === 'v' && ['true', 'on', 'yes', '1'].includes(row.invoker)
    if (!table && !view) unsafe.push(`${row.schema}.${row.relation}`)
  }
  return unsafe
}

export async function checkPublicRelations(connection, relations) {
  const client = new pg.Client({ connectionString: connection, statement_timeout: limits.queryMs, connectionTimeoutMillis: limits.healthProbeMs })
  await client.connect()
  try {
    const { rows } = await client.query(publicRelationsQuery, [relations.map(r => r.schema), relations.map(r => r.relation)])
    const unsafe = unsafePublicRelations(rows)
    if (unsafe.length) throw new Error(`Public reads require RLS tables or security-invoker views: ${unsafe.join(', ')}`)
  } finally { await client.end() }
}
