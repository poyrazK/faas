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

export async function inspect(connection, schemas) {
  const client = new pg.Client({ connectionString: databaseURL(connection), connectionTimeoutMillis: limits.queryMs, query_timeout: limits.queryMs })
  await client.connect()
  try {
    await client.query('BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY')
    await client.query("SELECT set_config('statement_timeout',$1,true)", [String(limits.queryMs)])
    const columns = (await client.query(columnsQuery, [schemas])).rows
    const types = (await client.query(typesQuery)).rows
    const relations = (await client.query(relationsQuery, [schemas])).rows
    if (columns.length > limits.columns || types.length > limits.types || relations.length > limits.relations) throw new Error('Schema exceeds generation limits')
    await client.query('COMMIT')
    return normalize(columns, types, relations, schemas)
  } finally { await client.end() }
}

export function normalize(columns, types, relationships, schemas) {
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
    const table = tables.get(`${r.schema}.${r.relation}`)
    const target = tables.get(`${r.referenced_schema}.${r.referenced_relation}`)
    if (!table || !target || !r.columns.every(n => table.columns.some(c => c.name === n)) || !r.referenced_columns.every(n => target.columns.some(c => c.name === n))) continue
    table.relationships.push({ foreignKeyName: r.name, columns: r.columns, isOneToOne: r.one_to_one, referencedRelation: r.referenced_relation, referencedColumns: r.referenced_columns })
  }
  return { version: 1, schemas: [...schemas].sort(), tables: [...tables.values()].sort((a, b) => `${a.schema}.${a.name}`.localeCompare(`${b.schema}.${b.name}`)), enums: Object.fromEntries(Object.entries(enums).sort(([a], [b]) => a.localeCompare(b))) }
}

export function generate(snapshot) {
  const hash = createHash('sha256').update(JSON.stringify(snapshot)).digest('hex')
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
    lines.push('    Functions: { [_ in never]: never }', '    Enums: {')
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
  process.stdout.write(generate(snapshot))
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) main().catch(() => { console.error('Data API schema generation failed'); process.exitCode = 1 })
