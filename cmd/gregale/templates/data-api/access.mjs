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

// Permissions narrow one server-key slot before admission; RLS still applies
// to whatever they allow. Read-only keys call only explicitly listed RPCs,
// because a function may write. Keys with allowlists cannot read the full
// OpenAPI catalog; the fingerprint-only serving contract stays readable.
export function serverKeyAllows(req, permissions, config, rpc) {
  if (!permissions) return true
  if (req.headers['x-gregale-subject'] !== undefined && !permissions.subjects) return false
  let url
  try { url = new URL(req.url, 'http://127.0.0.1') } catch { return false }
  if (url.pathname === '/__gregale/schema') return true
  if (url.pathname === '/openapi.json') return !permissions.relations && !permissions.rpcs
  const listed = (list, schema, name) => list.some(entry => entry.schema === schema && entry.name === name)
  if (rpc) {
    if (permissions.rpcs) return listed(permissions.rpcs, rpc.schema, rpc.name)
    return permissions.access === 'write'
  }
  const read = ['GET', 'HEAD'].includes(req.method)
  if (!read && permissions.access !== 'write') return false
  if (!permissions.relations) return true
  const schema = req.headers[read ? 'accept-profile' : 'content-profile'] ?? config.schemas[0]
  let relation
  try { relation = decodeURIComponent(url.pathname.slice('/rest/v1/'.length)) } catch { return false }
  if (!listed(permissions.relations, schema, relation)) return false
  const selects = url.searchParams.getAll('select')
  if (selects.length > 1) return false
  const embedded = selects.length ? selectEmbeddings(selects[0]) : []
  return embedded !== null && embedded.every(name => listed(permissions.relations, schema, name))
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
  const embedded = selects.length ? selectEmbeddings(selects[0]) : []
  if (!embedded || embedded.some(name => !listed(name))) return { code: 'public_read_not_allowed' }
  return { schema, relation, embedded }
}

export function publicRpcListed(config, rpc) {
  return Boolean(rpc && config.publicRpc?.some(entry => entry.schema === rpc.schema && entry.name === rpc.name))
}

// Anonymous RPCs are listed functions called by GET or HEAD, which PostgREST
// runs in a read-only transaction; a STABLE function can still call a VOLATILE
// one, so the transaction, not volatility, stops writes. Embedded relations
// must be publicly readable.
export function publicRpcTarget(req, config, rpc) {
  if (!publicRpcListed(config, rpc)) return { code: 'token_required' }
  if (!['GET', 'HEAD'].includes(req.method)) return { code: 'public_rpc_requires_get' }
  let url
  try { url = new URL(req.url, 'http://127.0.0.1') } catch { return { code: 'token_required' } }
  const selects = url.searchParams.getAll('select')
  if (selects.length > 1) return { code: 'public_read_not_allowed' }
  const embedded = selects.length ? selectEmbeddings(selects[0]) : []
  const readable = name => config.publicRead?.some(entry => entry.schema === rpc.schema && entry.relation === name)
  if (!embedded || !embedded.every(readable)) return { code: 'public_read_not_allowed' }
  return { schema: rpc.schema, rpc: rpc.name, embedded }
}

const jsonMediaRange = /^\s*(application\/json|text\/plain|\*\/\*)\s*(;[^,]*)?$/i

// The edge cache key covers method, host, path and query only. A response may
// become a shared entry only when those fully determine it: the default schema
// and the default full JSON representation. Everything else stays no-store.
export function publicReadCache(req, config, target) {
  if (!config.publicReadMaxAge || !(target?.relation || target?.rpc) || target.schema !== config.schemas[0]) return null
  const h = req.headers
  if (h.range !== undefined || h['range-unit'] !== undefined || h.prefer !== undefined || h.cookie !== undefined) return null
  if (h['accept-profile'] !== undefined && h['accept-profile'] !== config.schemas[0]) return null
  if (h.accept !== undefined && !h.accept.split(',').every(part => jsonMediaRange.test(part))) return null
  // Tags let owners purge one relation or function; every relation in the body
  // must be tagged. A function's own reads are not known, so its tag is separate.
  const own = target.rpc ? `data-api-rpc:${target.schema}.${target.rpc}` : `data-api:${target.schema}.${target.relation}`
  const tags = ['data-api', ...new Set([own, ...target.embedded.map(name => `data-api:${target.schema}.${name}`)])]
  if (tags.length > limits.cacheTagsMax || tags.some(tag => Buffer.byteLength(tag) > limits.cacheTagMaxBytes)) return null
  return Object.freeze({ cacheControl: `public, max-age=${config.publicReadMaxAge}`, tags: tags.join(', ') })
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

// PostgreSQL owns row decisions for RLS-enabled tables and security-invoker
// views. Policies themselves stay the owner's responsibility.
export function rowSecured(row) {
  return ['r', 'p'].includes(row.kind) && row.rls === true || row.kind === 'v' && ['true', 'on', 'yes', '1'].includes(row.invoker)
}

// Public reads require a row-secured relation for every listed relation.
export function unsafePublicRelations(rows) {
  return rows.filter(row => !rowSecured(row)).map(row => `${row.schema}.${row.relation}`)
}

const servingRelationsQuery = `SELECT n.nspname AS schema, c.relname AS relation, c.relkind AS kind, c.relrowsecurity AS rls,
 COALESCE((SELECT lower(o.option_value) FROM pg_options_to_table(c.reloptions) o WHERE o.option_name='security_invoker'),'') AS invoker
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname=ANY($1::text[]) AND c.relkind IN ('r','p','v','m','f')
 ORDER BY 1,2 LIMIT ${limits.relations + 1}`

// The exposed relations and whether each is row-secured, read at boot with the
// rest of the serving contract (ADR 20261026140223).
export async function loadRowSecurity(connection, schemas) {
  const client = new pg.Client({ connectionString: connection, statement_timeout: limits.queryMs, connectionTimeoutMillis: limits.healthProbeMs })
  await client.connect()
  try {
    const { rows } = await client.query(servingRelationsQuery, [schemas])
    if (rows.length > limits.relations) throw new Error('Too many exposed relations')
    return new Map(rows.map(row => [`${row.schema}.${row.relation}`, rowSecured(row)]))
  } finally { await client.end() }
}

// Signed-in requests reach only row-secured relations, or ones the owner lists
// in DATA_API_ALLOW_WITHOUT_RLS, as their target, embeddings and computed
// relationship targets. A relation the boot contract does not know is not
// served until a refresh. Returns the refusal, or null.
export function rowSecurityDenial(req, config, rpc) {
  const secured = config.rowSecurity
  if (!secured) return null
  let url
  try { url = new URL(req.url, 'http://127.0.0.1') } catch { return { status: 400, code: 'invalid_path' } }
  // /openapi.json and /__gregale/schema describe the contract; they read no rows.
  if (!url.pathname.startsWith('/rest/v1/')) return null
  const schema = rpc ? rpc.schema : req.headers[['GET', 'HEAD'].includes(req.method) ? 'accept-profile' : 'content-profile'] ?? config.schemas[0]
  const exempt = name => config.allowWithoutRLS?.some(entry => entry.schema === schema && entry.relation === name) ?? false
  const check = name => {
    const state = secured.get(`${schema}.${name}`)
    if (state === undefined) return { status: 404, code: 'not_found' }
    return state || exempt(name) ? null : { status: 403, code: 'rls_required' }
  }
  if (!rpc) {
    let relation
    try { relation = decodeURIComponent(url.pathname.slice('/rest/v1/'.length)) } catch { return { status: 400, code: 'invalid_path' } }
    // The bare /rest/v1/ root describes the schema; it reads no rows.
    const denial = relation ? check(relation) : null
    if (denial) return denial
  }
  for (const select of url.searchParams.getAll('select')) {
    const embedded = selectEmbeddings(select)
    if (!embedded) {
      // Select syntax this reader cannot classify is refused only where an
      // unprotected relation could be embedded.
      for (const [key, state] of secured) {
        if (!state && key.startsWith(`${schema}.`) && !exempt(key.slice(schema.length + 1))) return { status: 403, code: 'rls_required' }
      }
      continue
    }
    for (const name of embedded) {
      const computed = config.functions?.find(f => f.schema === schema && f.name === name && f.setof?.from)
      const denial = check(computed ? computed.setof.to : name)
      if (denial) return denial
    }
  }
  return null
}

const publicFunctionsQuery = `SELECT t.schema, t.name, p.provolatile AS volatility, p.prosecdef AS definer
 FROM unnest($1::text[],$2::text[]) AS t(schema,name)
 LEFT JOIN pg_namespace n ON n.nspname=t.schema
 LEFT JOIN pg_proc p ON p.pronamespace=n.oid AND p.proname=t.name`

// Public RPCs must be in the serving contract (annotated, one overload,
// security invoker) and declared STABLE or IMMUTABLE, so their intent is to
// read; the read-only transaction enforces it.
export function unsafePublicFunctions(entries, rows, approved) {
  const unsafe = []
  for (const entry of entries) {
    const found = rows.filter(row => row.schema === entry.schema && row.name === entry.name && row.volatility !== null)
    const callable = approved.some(f => f.schema === entry.schema && f.name === entry.name)
    if (!callable || found.length !== 1 || !['s', 'i'].includes(found[0].volatility) || found[0].definer !== false) unsafe.push(`${entry.schema}.${entry.name}`)
  }
  return unsafe
}

export async function checkPublicFunctions(connection, entries, approved) {
  const client = new pg.Client({ connectionString: connection, statement_timeout: limits.queryMs, connectionTimeoutMillis: limits.healthProbeMs })
  await client.connect()
  try {
    const { rows } = await client.query(publicFunctionsQuery, [entries.map(f => f.schema), entries.map(f => f.name)])
    const unsafe = unsafePublicFunctions(entries, rows, approved)
    if (unsafe.length) throw new Error(`Public RPCs require approved STABLE or IMMUTABLE functions: ${unsafe.join(', ')}`)
  } finally { await client.end() }
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
