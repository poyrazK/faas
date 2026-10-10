import pg from 'pg'
import { createHash } from 'node:crypto'
import { pathToFileURL } from 'node:url'
import { databaseURL, schemaNames, limits } from './config.mjs'

// Fixed, parameterized catalog reads run in the customer's workload using
// its restricted binding. No SQL text or database URL comes from a request.
const columnsQuery = `SELECT n.nspname AS schema, c.relname AS relation, c.relkind AS kind,
 a.attname AS name, a.atttypid::text AS type, a.attnotnull AS required,
 a.attidentity AS identity, a.attgenerated AS generated, d.oid IS NOT NULL AS defaulted,
 has_column_privilege(c.oid,a.attnum,'INSERT') AS insertable,
 has_column_privilege(c.oid,a.attnum,'UPDATE') AS updatable
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 JOIN pg_attribute a ON a.attrelid=c.oid
 LEFT JOIN pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum
 WHERE n.nspname=ANY($1::text[]) AND c.relkind IN ('r','p','v','m')
 AND a.attnum>0 AND NOT a.attisdropped AND has_schema_privilege(n.oid,'USAGE')
 AND has_column_privilege(c.oid,a.attnum,'SELECT')
 ORDER BY n.nspname,c.relname,a.attnum LIMIT ${limits.columns + 1}`

const typesQuery = `SELECT t.oid::text AS id, t.typname AS name, n.nspname AS schema,
 t.typtype AS kind, t.typbasetype::text AS base, t.typelem::text AS element,
 t.typnotnull AS required,
 ARRAY(SELECT e.enumlabel::text FROM pg_enum e WHERE e.enumtypid=t.oid ORDER BY e.enumsortorder) AS labels
 FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace
 ORDER BY t.oid LIMIT ${limits.types + 1}`

const relationsQuery = `SELECT n.nspname AS schema, c.relname AS relation, x.conname AS name,
 rn.nspname AS referenced_schema, rc.relname AS referenced_relation,
 ARRAY(SELECT a.attname::text FROM unnest(x.conkey) WITH ORDINALITY k(num,seq)
 JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum=k.num ORDER BY k.seq) AS columns,
 ARRAY(SELECT a.attname::text FROM unnest(x.confkey) WITH ORDINALITY k(num,seq)
 JOIN pg_attribute a ON a.attrelid=rc.oid AND a.attnum=k.num ORDER BY k.seq) AS referenced_columns,
 EXISTS(SELECT 1 FROM pg_constraint u WHERE u.conrelid=c.oid AND u.contype IN ('p','u')
 AND u.conkey @> x.conkey AND u.conkey <@ x.conkey) AS one_to_one
 FROM pg_constraint x JOIN pg_class c ON c.oid=x.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace
 JOIN pg_class rc ON rc.oid=x.confrelid JOIN pg_namespace rn ON rn.oid=rc.relnamespace
 WHERE x.contype='f' AND n.nspname=ANY($1::text[]) AND rn.nspname=ANY($1::text[])
 AND has_table_privilege(c.oid,'SELECT') AND has_table_privilege(rc.oid,'SELECT')
 ORDER BY n.nspname,c.relname,x.conname LIMIT ${limits.relations + 1}`

// An exact annotation is the owner's explicit RPC opt-in. Count every overload,
// including inaccessible ones: PostgREST dispatch must never select another body.
const functionsQuery = `SELECT n.nspname AS schema, p.proname AS name,
 p.prorettype::text AS return_type, p.proretset AS setof,
 p.proargnames AS names, p.proargtypes::oid[]::text[] AS arguments,
 p.pronargdefaults AS defaults, p.proargmodes AS modes,
 (SELECT count(*) FROM pg_proc other WHERE other.pronamespace=p.pronamespace AND other.proname=p.proname) AS overloads
 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname=ANY($1::text[]) AND p.prokind='f' AND NOT p.prosecdef
 AND p.provariadic=0 AND has_schema_privilege(n.oid,'USAGE')
 AND ($2::boolean OR has_function_privilege(p.oid,'EXECUTE'))
 AND obj_description(p.oid,'pg_proc')='@gregale:rpc'
 ORDER BY n.nspname,p.proname LIMIT ${limits.relations + 1}`

export async function inspect(connection, schemas) {
  const client = new pg.Client({ connectionString: databaseURL(connection), connectionTimeoutMillis: limits.queryMs, query_timeout: limits.queryMs })
  await client.connect()
  try {
    await client.query('BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY')
    await client.query("SELECT set_config('statement_timeout',$1,true)", [String(limits.queryMs)])
    const snapshot = await inspectClient(client, schemas)
    await client.query('COMMIT')
    return snapshot
  } finally { await client.end() }
}

export async function inspectClient(client, schemas, { includeUnexecutable = false } = {}) {
  const columns = (await client.query(columnsQuery, [schemas])).rows
  const types = (await client.query(typesQuery)).rows
  const relations = (await client.query(relationsQuery, [schemas])).rows
  if (columns.length > limits.columns || types.length > limits.types || relations.length > limits.relations) throw new Error('Schema exceeds generation limits')
  const functions = (await client.query(functionsQuery, [schemas, includeUnexecutable])).rows
  if (functions.length > limits.relations) throw new Error('Too many opted-in functions')
  return normalize(columns, types, relations, schemas, functions)
}

export function normalize(columns, types, relationships, schemas, functions = []) {
  const byID = new Map(types.map(t => [t.id, t]))
  const enums = {}
  const seen = new Set()
  function tsType(id, depth = 0) {
    const t = byID.get(id)
    if (!t || depth > 12) return 'unknown'
    if (t.kind === 'd') return tsType(t.base, depth + 1)
    if (t.kind === 'e') {
      if (schemas.includes(t.schema)) enums[`${t.schema}.${t.name}`] = t.labels
      return t.labels.map(JSON.stringify).join(' | ') || 'never'
    }
    if (t.element !== '0') return `(${tsType(t.element, depth + 1)} | null)[]`
    if (t.schema !== 'pg_catalog') return 'unknown'
    if (['int2', 'int4', 'int8', 'float4', 'float8', 'numeric', 'oid'].includes(t.name)) return 'number'
    if (t.name === 'bool') return 'boolean'
    if (['json', 'jsonb'].includes(t.name)) return 'Json'
    if (['text', 'varchar', 'bpchar', 'uuid', 'date', 'timestamp', 'timestamptz', 'time', 'timetz', 'interval', 'bytea', 'name', 'inet', 'cidr', 'macaddr', 'macaddr8', 'money', 'bit', 'varbit', 'xml', 'tsvector', 'tsquery'].includes(t.name)) return 'string'
    return 'unknown'
  }
  const tables = new Map()
  for (const c of columns) {
    const key = `${c.schema}.${c.relation}`
    seen.add(key)
    if (seen.size > limits.relations) throw new Error('Too many exposed relations')
    const table = tables.get(key) ?? { schema: c.schema, name: c.relation, view: ['v', 'm'].includes(c.kind), columns: [], relationships: [] }
    const column = { name: c.name, type: tsType(c.type), nullable: !(c.required || byID.get(c.type)?.required),
      optional: c.defaulted || c.identity !== '' || !(c.required || byID.get(c.type)?.required),
      insertable: c.insertable && !c.generated && c.identity !== 'a', updatable: c.updatable && !c.generated && c.identity !== 'a' }
    table.columns.push(column)
    tables.set(key, table)
  }
  for (const r of relationships) {
    // postgrest-js resolves a relationship in the selected schema's type map.
    // A cross-schema FK could otherwise falsely target a same-named local table.
    if (r.schema !== r.referenced_schema) continue
    const table = tables.get(`${r.schema}.${r.relation}`)
    const target = tables.get(`${r.referenced_schema}.${r.referenced_relation}`)
    if (!table || !target || !r.columns.every(n => table.columns.some(c => c.name === n)) || !r.referenced_columns.every(n => target.columns.some(c => c.name === n))) continue
    table.relationships.push({ foreignKeyName: r.name, columns: r.columns, isOneToOne: r.one_to_one, referencedRelation: r.referenced_relation, referencedColumns: r.referenced_columns })
  }
  function supported(id, depth = 0) {
    const type = byID.get(id)
    if (!type || depth > 12) return false
    if (type.kind === 'd') return supported(type.base, depth + 1)
    if (type.kind === 'e') return type.labels.length > 0
    if (type.element !== '0') return supported(type.element, depth + 1)
    return tsType(id) !== 'unknown'
  }
  const callable = []
  for (const f of functions) {
    if (Number(f.overloads) !== 1 || !/^[a-z][a-z0-9_]{0,62}$/.test(f.name) ||
        f.modes?.some(mode => mode !== 'i') || (f.names ?? []).length !== f.arguments.length || new Set(f.names ?? []).size !== f.arguments.length) continue
    const args = f.arguments.map((id, i) => ({ name: f.names[i], type: tsType(id), optional: i >= f.arguments.length - f.defaults }))
    if (args.some(a => !a.name) || f.arguments.some(id => !supported(id))) continue
    const result = byID.get(f.return_type)
    const table = result?.kind === 'c' ? tables.get(`${result.schema}.${result.name}`) : null
    if (result?.kind === 'c' && (!table || !f.setof)) continue
    let returns = table ? `Database[${JSON.stringify(table.schema)}][${JSON.stringify(table.view ? 'Views' : 'Tables')}][${JSON.stringify(table.name)}]['Row']` : tsType(f.return_type)
    if (result?.schema === 'pg_catalog' && result.name === 'void') returns = 'undefined'
    if (!table && returns !== 'undefined' && !supported(f.return_type)) continue
    returns = f.setof ? table ? `${returns}[]` : `(${returns} | null)[]` : returns === 'undefined' ? returns : `${returns} | null`
    callable.push({ schema: f.schema, name: f.name, args, returns })
  }
  return { version: 1, schemas: [...schemas].sort(), tables: [...tables.values()].sort((a, b) => `${a.schema}.${a.name}`.localeCompare(`${b.schema}.${b.name}`)), functions: callable.sort((a, b) => `${a.schema}.${a.name}`.localeCompare(`${b.schema}.${b.name}`)), enums: Object.fromEntries(Object.entries(enums).sort(([a], [b]) => a.localeCompare(b))) }
}

export function fingerprint(snapshot) {
  return createHash('sha256').update(JSON.stringify(snapshot)).digest('hex')
}

export function generate(snapshot) {
  const hash = fingerprint(snapshot)
  const lines = [`// Generated by gregale data-api types. Do not edit.`, `// Schema fingerprint: ${hash}`, 'export type Json = string | number | boolean | null | { [key: string]: Json | undefined } | Json[]', 'export type Database = {']
  for (const schema of snapshot.schemas) {
    lines.push(`  ${JSON.stringify(schema)}: {`)
    for (const [group, view] of [['Tables', false], ['Views', true]]) {
      lines.push(`    ${group}: {`)
      for (const t of snapshot.tables.filter(t => t.schema === schema && t.view === view)) {
        lines.push(`      ${JSON.stringify(t.name)}: {`)
        for (const kind of ['Row', 'Insert', 'Update']) {
          if (view && kind !== 'Row') continue
          lines.push(`        ${kind}: {`)
          for (const c of t.columns) {
            const allowed = kind === 'Row' || (kind === 'Insert' ? c.insertable : c.updatable)
            const optional = kind !== 'Row' && (!allowed || kind === 'Update' || c.optional)
            const type = allowed ? `${c.type}${c.nullable ? ' | null' : ''}` : 'never'
            lines.push(`          ${JSON.stringify(c.name)}${optional ? '?' : ''}: ${type}`)
          }
          lines.push('        }')
        }
        lines.push(`        Relationships: ${JSON.stringify(t.relationships)}`, '      }')
      }
      lines.push('    }')
    }
    lines.push('    Functions: {')
    for (const f of snapshot.functions ?? []) if (f.schema === schema) {
      lines.push(`      ${JSON.stringify(f.name)}: {`, '        Args: {')
      for (const a of f.args) lines.push(`          ${JSON.stringify(a.name)}${a.optional ? '?' : ''}: ${a.type} | null`)
      lines.push('        }', `        Returns: ${f.returns}`, '      }')
    }
    lines.push('    }', '    Enums: {')
    for (const [key, labels] of Object.entries(snapshot.enums)) if (key.startsWith(`${schema}.`)) lines.push(`      ${JSON.stringify(key.slice(schema.length + 1))}: ${labels.map(JSON.stringify).join(' | ')}`)
    lines.push('    }', '    CompositeTypes: Record<string, never>', '  }')
  }
  lines.push('}', '')
  const result = lines.join('\n')
  if (Buffer.byteLength(result) > limits.outputBytes) throw new Error('Generated contract exceeds output limit')
  return result
}

export async function main(env = process.env) {
  const snapshot = await inspect(env.DATABASE_URL, schemaNames(env.DATA_API_SCHEMAS))
  const output = process.argv.includes('--snapshot') ? JSON.stringify({ version: 1, fingerprint: fingerprint(snapshot), snapshot }) + '\n' : generate(snapshot)
  if (Buffer.byteLength(output) > limits.outputBytes) throw new Error('Generated contract exceeds output limit')
  process.stdout.write(output)
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) main().catch(() => { console.error('Data API schema generation failed'); process.exitCode = 1 })

// OpenAPI uses the same normalized contract as TypeScript generation. Keep
// PostgreSQL expressions and internal catalog identifiers out of the document.
export function contractSchema(type) {
  if (type.endsWith(' | null')) return { ...contractSchema(type.slice(0, -7)), 'x-nullable': true }
  if (type.endsWith(')[]') && type.startsWith('(')) return { type: 'array', items: contractSchema(type.slice(1, -3)) }
  const row = /^Database\[("(?:[^"\\]|\\.)*")\]\[("Tables"|"Views")\]\[("(?:[^"\\]|\\.)*")\]\['Row'\](\[\])?$/.exec(type)
  if (row) {
    const ref = { $ref: `#/definitions/${pointer(`${JSON.parse(row[1])}.${JSON.parse(row[3])}.Row`)}` }
    return row[4] ? { type: 'array', items: ref } : ref
  }
  if (type.startsWith('"')) {
    try { return { type: 'string', enum: JSON.parse(`[${(type.match(/"(?:[^"\\]|\\.)*"/g) ?? []).join(',')}]`) } } catch { return {} }
  }
  if (['string', 'number', 'boolean'].includes(type)) return { type }
  // Json and unknown deliberately accept every JSON shape; void has no body.
  return {}
}

function pointer(value) { return value.replaceAll('~', '~0').replaceAll('/', '~1') }

export function enrichOpenAPI(document, snapshot, schema) {
  if (!snapshot) return document
  document['x-gregale-schema-fingerprint'] = fingerprint(snapshot)
  document.securityDefinitions = { bearer: { type: 'apiKey', name: 'Authorization', in: 'header', description: 'Bearer <application JWT>; issuer, audience, expiry and subject are verified. Row-level security applies to the subject.' } }
  document.security = [{ bearer: [] }]
  document.definitions ??= {}
  const ref = name => ({ $ref: `#/definitions/${pointer(name)}` })
  const object = (columns, kind) => {
    const properties = Object.fromEntries(columns.filter(c => kind === 'Row' || (kind === 'Insert' ? c.insertable : c.updatable)).map(c => [c.name, { ...contractSchema(c.type), ...(c.nullable ? { 'x-nullable': true } : {}), ...(kind === 'Insert' && c.optional ? { description: 'May be omitted; the database default or null applies.' } : {}) }]))
    const required = columns.filter(c => kind === 'Row' || kind === 'Insert' && c.insertable && !c.optional).map(c => c.name)
    return { type: 'object', properties, ...(required.length ? { required } : {}), ...(kind === 'Row' ? {} : { additionalProperties: false }) }
  }
  for (const table of snapshot.tables.filter(t => t.schema === schema)) {
    for (const kind of table.view ? ['Row'] : ['Row', 'Insert', 'Update']) document.definitions[`${schema}.${table.name}.${kind}`] = object(table.columns, kind)
    const path = document.paths?.[`/${table.name}`]
    if (!path) continue
    for (const method of ['get', 'post', 'patch', 'delete']) {
      const operation = path[method]
      if (!operation) continue
      if (!table.view && (method === 'post' || method === 'patch')) {
        const body = operation.parameters?.find(p => p.in === 'body')
        if (body) {
          const value = ref(`${schema}.${table.name}.${method === 'post' ? 'Insert' : 'Update'}`)
          // Swagger 2 cannot express object-or-array unions; this extension
          // documents PostgREST bulk bodies without dropping single-row clients.
          body.schema = { ...value, 'x-gregale-bulk-schema': { type: 'array', items: value } }
        }
      }
      for (const [status, response] of Object.entries(operation.responses ?? {})) if (/^2/.test(status) && status !== '204') response.schema = { type: 'array', items: ref(`${schema}.${table.name}.Row`) }
      operation.description = `${operation.description ?? ''}\nRow-level security controls visibility. Use select for projections, column=operator.value for filters, order, limit and offset for pagination. Range and Prefer: count=exact enable Content-Range totals; Prefer: return=representation requests write rows. Projections and object media types change the response shape.`.trim()
    }
  }
  for (const fn of (snapshot.functions ?? []).filter(f => f.schema === schema)) {
    const operation = document.paths?.[`/rpc/${fn.name}`]?.post
    if (!operation) continue
    const name = `${schema}.${fn.name}.Args`
    const required = fn.args.filter(a => !a.optional).map(a => a.name)
    document.definitions[name] = { type: 'object', properties: Object.fromEntries(fn.args.map(a => [a.name, { ...contractSchema(a.type), 'x-nullable': true }])), ...(required.length ? { required } : {}), additionalProperties: false }
    const body = operation.parameters?.find(p => p.in === 'body')
    if (body) body.schema = ref(name)
    else {
      operation.parameters ??= []
      operation.parameters.push({ name: 'args', in: 'body', required: required.length > 0, schema: ref(name) })
    }
    for (const [status, response] of Object.entries(operation.responses ?? {})) if (/^2/.test(status) && status !== '204') {
      if (fn.returns === 'undefined') delete response.schema
      else response.schema = contractSchema(fn.returns)
    }
  }
  document.definitions.GregaleProblem = { type: 'object', properties: { type: { type: 'string' }, title: { type: 'string' }, status: { type: 'integer' }, code: { type: 'string' }, request_id: { type: 'string', format: 'uuid' } }, required: ['type', 'title', 'status', 'code', 'request_id'] }
  for (const path of Object.values(document.paths ?? {})) for (const method of ['get', 'post', 'patch', 'delete']) {
    const operation = path[method]
    if (!operation) continue
    operation.responses ??= {}
    for (const status of ['401', '413', '503', '504']) operation.responses[status] ??= { description: 'Gateway authentication, body limit, availability or timeout failure (application/problem+json). Database failures use the PostgREST error shape.', schema: ref('GregaleProblem') }
    for (const response of Object.values(operation.responses)) {
      response.headers ??= {}
      response.headers['X-Request-Id'] = { type: 'string', description: 'Server-generated UUID; correlate with data_api_request runtime logs.' }
    }
  }
  return document
}
