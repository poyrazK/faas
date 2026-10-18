import pg from 'pg'
import { fingerprint, generate, generateValidators } from './type-render.mjs'
export { fingerprint, generate } from './type-render.mjs'
import { pathToFileURL } from 'node:url'
import { databaseURL, schemaNames, limits } from './config.mjs'

// Fixed, parameterized catalog reads run in the customer's workload using
// its restricted binding. No SQL text or database URL comes from a request.
const columnsQuery = `SELECT n.nspname AS schema, c.relname AS relation, c.relkind AS kind,
 c.oid::text AS id, a.attnum AS number,
 a.attname AS name, a.atttypid::text AS type, a.attnotnull AS required,
 a.attidentity AS identity, a.attgenerated AS generated, d.oid IS NOT NULL AS defaulted,
 has_column_privilege(COALESCE($2::name,current_user),c.oid,a.attnum,'INSERT') AS insertable,
 has_column_privilege(COALESCE($2::name,current_user),c.oid,a.attnum,'UPDATE') AS updatable
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 JOIN pg_attribute a ON a.attrelid=c.oid
 LEFT JOIN pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum
 WHERE n.nspname=ANY($1::text[]) AND c.relkind IN ('r','p','v','m')
 AND a.attnum>0 AND NOT a.attisdropped AND has_schema_privilege(COALESCE($2::name,current_user),n.oid,'USAGE')
 AND has_column_privilege(COALESCE($2::name,current_user),c.oid,a.attnum,'SELECT')
 ORDER BY n.nspname,c.relname,a.attnum LIMIT ${limits.columns + 1}`

const typesQuery = `SELECT t.oid::text AS id, t.typname AS name, n.nspname AS schema,
 t.typtype AS kind, t.typbasetype::text AS base, t.typelem::text AS element,
 t.typnotnull AS required,
 has_schema_privilege(COALESCE($1::name,current_user),n.oid,'USAGE')
 AND has_type_privilege(COALESCE($1::name,current_user),t.oid,'USAGE') AS accessible,
 CASE WHEN c.relkind IN ('r','p','v','m') THEN
 has_table_privilege(COALESCE($1::name,current_user),c.oid,'SELECT') ELSE false END AS selectable,
 ARRAY(SELECT e.enumlabel::text FROM pg_enum e WHERE e.enumtypid=t.oid ORDER BY e.enumsortorder) AS labels
 FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace LEFT JOIN pg_class c ON c.oid=t.typrelid
 ORDER BY t.oid LIMIT ${limits.types + 1}`

// Standalone CREATE TYPE ... AS (...) objects only. Table row types must keep
// using the privilege-filtered relation contract, never unrestricted attributes.
const compositesQuery = `SELECT t.oid::text AS id, n.nspname AS schema, t.typname AS name,
 a.attname AS field, a.atttypid::text AS type, a.attnotnull AS required
 FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace
 JOIN pg_class c ON c.oid=t.typrelid AND c.relkind='c'
 LEFT JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped
 WHERE n.nspname=ANY($1::text[]) AND has_schema_privilege(COALESCE($2::name,current_user),n.oid,'USAGE')
 AND has_type_privilege(COALESCE($2::name,current_user),t.oid,'USAGE')
 ORDER BY n.nspname,t.typname,a.attnum LIMIT ${limits.columns + 1}`

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
 WHERE x.contype='f' AND x.conparentid=0 AND n.nspname=ANY($1::text[]) AND rn.nspname=ANY($1::text[])
 AND has_table_privilege(COALESCE($2::name,current_user),c.oid,'SELECT') AND has_table_privilege(COALESCE($2::name,current_user),rc.oid,'SELECT')
 ORDER BY n.nspname,c.relname,x.conname LIMIT ${limits.relations + 1}`

// The engine follows resorigtbl/resorigcol in pg_rewrite's root target list.
// Budget the complete input before returning definitions to the workload.
const viewsQuery = `WITH views AS (
 SELECT c.oid::text AS id,n.nspname AS schema,c.relname AS relation,r.ev_action::text AS action
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 JOIN pg_rewrite r ON r.ev_class=c.oid AND r.rulename='_RETURN'
 WHERE c.relkind='v' AND n.nspname=ANY($1::text[])
 AND has_schema_privilege(COALESCE($2::name,current_user),n.oid,'USAGE') AND has_any_column_privilege(COALESCE($2::name,current_user),c.oid,'SELECT')
), budget AS (SELECT sum(octet_length(action)) AS bytes FROM views)
 SELECT id,schema,relation,CASE WHEN bytes<=${limits.outputBytes} THEN action ELSE NULL END AS action
 FROM views CROSS JOIN budget ORDER BY schema,relation LIMIT ${limits.relations + 1}`

// Read PostgreSQL's structural node format, never SQL text or view names.
// Escaped delimiters stay inside their token; nested query targets cannot be
// mistaken for the root projection. Unsupported definitions have no lineage.
export function viewOrigins(action) {
  if (typeof action !== 'string' || Buffer.byteLength(action) > limits.outputBytes) return []
  try {
    const tokens = []
    for (let i = 0; i < action.length;) {
      if (/\s/.test(action[i])) { i++; continue }
      if ('(){}'.includes(action[i])) { tokens.push(action[i++]); continue }
      const start = i
      while (i < action.length && !/\s/.test(action[i]) && !'(){}'.includes(action[i])) {
        if (action[i] === '\\') { if (++i === action.length) throw new Error('Invalid token') }
        i++
      }
      tokens.push(action.slice(start, i))
    }
    function end(start) {
      const pairs = { '(': ')', '{': '}' }, stack = []
      if (!pairs[tokens[start]]) return start + 1
      for (let i = start; i < tokens.length; i++) {
        const token = tokens[i]
        if (pairs[token]) stack.push(pairs[token])
        else if (token === ')' || token === '}') {
          if (stack.pop() !== token) throw new Error('Invalid nesting')
          if (!stack.length) return i + 1
        }
      }
      throw new Error('Incomplete node')
    }
    function node(start, tag) {
      if (tokens[start] !== '{' || tokens[start + 1] !== tag) throw new Error('Invalid node')
      const stop = end(start), fields = new Map()
      for (let i = start + 2; i < stop - 1;) {
        const key = tokens[i++]
        if (!key.startsWith(':')) throw new Error('Invalid field')
        const value = i, next = end(i)
        if (next >= stop || fields.has(key)) throw new Error('Invalid value')
        fields.set(key, [value, next])
        i = next
      }
      return fields
    }
    if (tokens[0] !== '(' || tokens.at(-1) !== ')' || end(1) !== tokens.length - 1) return []
    const query = node(1, 'QUERY')
    const scalar = (fields, name) => {
      const range = fields.get(name)
      return range && range[1] === range[0] + 1 ? tokens[range[0]] : undefined
    }
    if (scalar(query, ':commandType') !== '1' || scalar(query, ':setOperations') !== '<>') return []
    const target = query.get(':targetList')
    if (!target || tokens[target[0]] !== '(') return []
    const origins = []
    for (let i = target[0] + 1; i < target[1] - 1; i = end(i)) {
      const entry = node(i, 'TARGETENTRY')
      const integer = key => {
        const value = scalar(entry, key)
        return /^[1-9][0-9]*$/.test(value ?? '') && Number.isSafeInteger(Number(value)) ? Number(value) : 0
      }
      const number = integer(':resno'), id = integer(':resorigtbl'), column = integer(':resorigcol')
      if (number && id && column && scalar(entry, ':resjunk') === 'false') origins.push({ number, id: String(id), column })
    }
    return origins
  } catch { return [] }
}

export function viewRelationships(columns, relationships, views, exposedSchemas=null) {
  const readable = new Map(columns.filter(c => c.id && c.number).map(c => [`${c.id}:${c.number}`, c]))
  const origins = new Map(), projected = new Map()
  for (const view of views) for (const origin of viewOrigins(view.action)) {
    const key = `${view.id}:${origin.number}`
    if (readable.has(key)) origins.set(key, `${origin.id}:${origin.column}`)
  }
  function base(key) {
    const seen = new Set()
    while (origins.has(key)) {
      if (seen.has(key) || seen.size > 12) return null
      seen.add(key); key = origins.get(key)
      if (!readable.has(key)) return null
    }
    const column = readable.get(key)
    return column && ['r', 'p'].includes(column.kind) ? key : null
  }
  for (const view of views) {
    const mapping = new Map()
    for (const c of columns.filter(c => c.id === view.id)) {
      const key = base(`${c.id}:${c.number}`)
      if (key) mapping.set(key, [...(mapping.get(key) ?? []), c.name])
    }
    projected.set(view.id, mapping)
  }
  function candidates(schema, relation, names) {
    const keys = names.map(name => columns.find(c => c.schema === schema && c.relation === relation && c.name === name)).map(c => c && `${c.id}:${c.number}`)
    if (keys.some(key => !key)) return []
    const result = !exposedSchemas||exposedSchemas.includes(schema)?[{ schema, relation, columns: names }]:[]
    for (const view of views.filter(v => exposedSchemas?exposedSchemas.includes(v.schema):v.schema === schema)) {
      const options = keys.map(key => projected.get(view.id).get(key))
      if (options.some(names => !names?.length)) continue
      let combinations = [[]]
      for (const names of options) {
        if (combinations.length * names.length > limits.relations) throw new Error('Too many inferred view relationships')
        combinations = combinations.flatMap(prefix => names.map(name => [...prefix, name]))
      }
      result.push(...combinations.map(columns => ({ schema:view.schema, relation: view.relation, columns })))
      if (result.length > limits.relations) throw new Error('Too many inferred view relationships')
    }
    return result
  }
  const inferred = []
  for (const r of relationships) {
    if (r.schema !== r.referenced_schema) continue
    const sources = candidates(r.schema, r.relation, r.columns), targets = candidates(r.referenced_schema, r.referenced_relation, r.referenced_columns)
    for (const source of sources) for (const target of targets) {
      if (source.schema===r.schema && source.relation === r.relation && target.schema===r.referenced_schema && target.relation === r.referenced_relation) continue
      if(source.schema!==target.schema)continue
      inferred.push({ ...r, schema: source.schema, relation: source.relation, columns: source.columns, referenced_schema: target.schema, referenced_relation: target.relation, referenced_columns: target.columns })
      if (inferred.length + relationships.length > limits.relations) throw new Error('Too many inferred view relationships')
    }
  }
  return inferred
}

// An exact annotation is the owner's explicit RPC opt-in. Count every overload,
// including inaccessible ones: PostgREST dispatch must never select another body.
const functionsQuery = `SELECT n.nspname AS schema, p.proname AS name,
 p.prorettype::text AS return_type, p.proretset AS setof,
 p.proargnames AS names, p.proargtypes::oid[]::text[] AS arguments,
 p.pronargdefaults AS defaults, p.proargmodes AS modes,
 p.provolatile AS volatility, p.prorows AS rows,
 (SELECT count(*) FROM pg_proc other WHERE other.pronamespace=p.pronamespace AND other.proname=p.proname) AS overloads
 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname=ANY($1::text[]) AND p.prokind='f' AND NOT p.prosecdef
 AND p.provariadic=0 AND has_schema_privilege(COALESCE($3::name,current_user),n.oid,'USAGE')
 AND ($2::boolean OR has_function_privilege(COALESCE($3::name,current_user),p.oid,'EXECUTE'))
 AND obj_description(p.oid,'pg_proc')='@gregale:rpc'
 ORDER BY n.nspname,p.proname LIMIT ${limits.relations + 1}`

// A computed relationship is callable inside select, outside the RPC route
// allowlist. Its serving-role EXECUTE grant must agree with the approved
// contract. Inspect every engine-recognized signature, including unsupported
// bodies, overloads and annotations, without exporting their definitions.
export async function computedPermissionBlockers(client, schemas, role, snapshot) {
  const candidates = (await client.query(`SELECT n.nspname AS schema,p.proname AS name
    FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
    JOIN pg_class source ON source.reltype=p.proargtypes[0] AND source.relkind IN ('r','p','v','m','f')
    JOIN pg_namespace source_schema ON source_schema.oid=source.relnamespace
    JOIN pg_class target ON target.reltype=p.prorettype AND target.relkind IN ('r','p','v','m','f')
    WHERE n.nspname=ANY($1::text[]) AND source_schema.nspname=ANY($1::text[]) AND p.pronargs=1
    AND has_schema_privilege(COALESCE($2::name,current_user),n.oid,'USAGE')
    AND has_function_privilege(COALESCE($2::name,current_user),p.oid,'EXECUTE')
    ORDER BY n.nspname,p.proname,p.oid LIMIT ${limits.relations + 1}`, [schemas, role])).rows
  if (candidates.length > limits.relations) throw new Error('Too many executable computed relationships')
  const approved = new Set(snapshot.functions.filter(f => f.setof?.from).map(f => `${f.schema}.${f.name}`))
  return [...new Set(candidates.filter(f => !approved.has(`${f.schema}.${f.name}`)).map(f => `computed_relationship_not_approved:${f.schema}.${f.name}`))]
}

export async function inspect(connection, schemas) {
  return (await inspectBundle(connection, schemas)).snapshot
}

export async function inspectBundle(connection, schemas, { validators = false } = {}) {
  const client = new pg.Client({ connectionString: databaseURL(connection), connectionTimeoutMillis: limits.queryMs, query_timeout: limits.queryMs })
  await client.connect()
  try {
    await client.query('BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY')
    await client.query("SELECT set_config('statement_timeout',$1,true)", [String(limits.queryMs)])
    const snapshot = await inspectClient(client, schemas)
    const metadata = validators ? await inspectInputMetadata(client, schemas, snapshot) : []
    await client.query('COMMIT')
    return { snapshot, metadata }
  } finally { await client.end() }
}

// Only structural metadata is exported, never CHECK expressions or SQL bodies.
export async function inspectInputMetadata(client, schemas, snapshot) {
  const rows = (await client.query(`SELECT n.nspname AS schema,c.relname AS relation,a.attname AS name,
    t.typname AS type,t.typtype='d' AS domain,a.atttypmod AS typmod,
    EXISTS(SELECT 1 FROM pg_constraint x WHERE x.conrelid=c.oid AND x.contype='c') AS checks
    FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
    JOIN pg_attribute a ON a.attrelid=c.oid JOIN pg_type t ON t.oid=a.atttypid
    WHERE n.nspname=ANY($1::text[]) AND c.relkind IN ('r','p') AND a.attnum>0 AND NOT a.attisdropped
    AND has_schema_privilege(n.oid,'USAGE') AND has_column_privilege(c.oid,a.attnum,'SELECT')
    ORDER BY n.nspname,c.relname,a.attnum LIMIT ${limits.columns + 1}`, [schemas])).rows
  if (rows.length > limits.columns) throw new Error('Schema exceeds input metadata limits')
  return rows.filter(c => snapshot.tables.some(t => t.schema === c.schema && t.name === c.relation && t.columns.some(f => f.name === c.name)))
}

export async function inspectRowMetadata(client, schemas, snapshot, privilegeRole = null) {
  const rows = (await client.query(`SELECT n.nspname AS schema,c.relname AS relation,
    ARRAY(SELECT a.attname::text FROM unnest(p.conkey) WITH ORDINALITY k(num,seq)
      JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum=k.num ORDER BY k.seq) AS columns,
    ARRAY(SELECT tn.nspname||'.'||t.typname FROM unnest(p.conkey) WITH ORDINALITY k(num,seq)
      JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum=k.num JOIN pg_type t ON t.oid=a.atttypid JOIN pg_namespace tn ON tn.oid=t.typnamespace ORDER BY k.seq) AS types,
    ARRAY(SELECT a.attname::text FROM pg_attribute a JOIN pg_type t ON t.oid=a.atttypid
      JOIN pg_namespace tn ON tn.oid=t.typnamespace WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped
      AND a.attnotnull AND tn.nspname='pg_catalog' AND t.typname IN ('int2','int4','int8')
      AND col_description(c.oid,a.attnum)='@gregale:version'
      AND EXISTS(SELECT 1 FROM pg_trigger g WHERE g.tgrelid=c.oid AND NOT g.tgisinternal AND g.tgenabled IN ('O','A')
        AND (g.tgtype::int & 19)=19 AND cardinality(g.tgattr::smallint[])=0 AND g.tgqual IS NULL)
      ORDER BY a.attnum) AS versions
    FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
    LEFT JOIN pg_constraint p ON p.conrelid=c.oid AND p.contype='p'
    WHERE n.nspname=ANY($1::text[]) AND c.relkind IN ('r','p') AND has_schema_privilege(COALESCE($2::name,current_user),n.oid,'USAGE')
    ORDER BY n.nspname,c.relname LIMIT ${limits.relations + 1}`, [schemas, privilegeRole])).rows
  if(rows.length>limits.relations) throw new Error('Too many row metadata relations')
  const safe=new Set(['int2','int4','int8','oid','bool','text','varchar','bpchar','uuid','date','timestamp','timestamptz','time','timetz'].map(name=>'pg_catalog.'+name))
  for(const row of rows){
    const table=snapshot.tables.find(table=>!table.view&&table.schema===row.schema&&table.name===row.relation)
    if(!table)continue
    if(row.columns.length&&row.columns.length<=16&&row.columns.every(name=>table.columns.some(c=>c.name===name&&!c.nullable)))
      table.primary_key={columns:row.columns,filterable:row.types.every(type=>safe.has(type))&&row.columns.every(name=>table.columns.some(c=>c.name===name&&['string','number','boolean'].includes(c.type)))}
    if(row.versions.length===1&&table.primary_key&&!table.primary_key.columns.includes(row.versions[0])&&table.columns.some(c=>c.name===row.versions[0]&&c.type==='number'&&!c.nullable))
      table.concurrency={column:row.versions[0],kind:'integer_version'}
  }
  snapshot.version=4
}

export async function inspectClient(client, schemas, { includeUnexecutable = false, privilegeRole = null } = {}) {
  const columns = (await client.query(columnsQuery, [schemas, privilegeRole])).rows
  const types = (await client.query(typesQuery, [privilegeRole])).rows
  const relations = (await client.query(relationsQuery, [schemas, privilegeRole])).rows
  if (columns.length > limits.columns || types.length > limits.types || relations.length > limits.relations) throw new Error('Schema exceeds generation limits')
  const functions = (await client.query(functionsQuery, [schemas, includeUnexecutable, privilegeRole])).rows
  if (functions.length > limits.relations) throw new Error('Too many opted-in functions')
  const composites = (await client.query(compositesQuery, [schemas, privilegeRole])).rows
  if (columns.length + composites.length > limits.columns) throw new Error('Schema exceeds generation limits')
  const views = (await client.query(viewsQuery, [schemas, privilegeRole])).rows
  if (views.length > limits.relations || views.some(v => v.action === null)) throw new Error('View lineage exceeds generation limits')
  // Private base columns may contribute FK lineage only when individually readable;
  // they never enter the exposed contract or generated table types.
  const baseIDs=[...new Set(views.flatMap(view=>viewOrigins(view.action).map(origin=>origin.id)).filter(id=>!columns.some(column=>column.id===id)))]
  if(baseIDs.length>limits.relations)throw new Error('Too many private view origins')
  const privateColumns=baseIDs.length?(await client.query(columnsQuery.replace('n.nspname=ANY($1::text[])',"c.oid=ANY($1::oid[]) AND c.relkind IN ('r','p')"),[baseIDs,privilegeRole])).rows:[]
  const privateRelations=baseIDs.length?(await client.query(relationsQuery.replace('n.nspname=ANY($1::text[]) AND rn.nspname=ANY($1::text[])','c.oid=ANY($1::oid[]) AND rc.oid=ANY($1::oid[])').replaceAll('has_table_privilege','has_any_column_privilege'),[baseIDs,privilegeRole])).rows:[]
  if(columns.length+privateColumns.length>limits.columns||relations.length+privateRelations.length>limits.relations)throw new Error('Private view lineage exceeds generation limits')
  const inferred=viewRelationships([...columns,...privateColumns],[...relations,...privateRelations],views,schemas)
  const snapshot = normalize(columns, types, [...relations, ...inferred], schemas, functions, composites)
  if (!includeUnexecutable && (await computedPermissionBlockers(client, schemas, privilegeRole, snapshot)).length) throw new Error('Unapproved computed relationship execution; revoke serving-role grants and PUBLIC execution or approve a supported signature')
  await inspectRowMetadata(client, schemas, snapshot, privilegeRole)
  return snapshot
}

export function normalize(columns, types, relationships, schemas, functions = [], compositeFields = []) {
  const byID = new Map(types.map(t => [t.id, t]))
  const compositeByID = new Map()
  for (const field of compositeFields) {
    const type = byID.get(field.id)
    if (type?.kind !== 'c' || type.accessible === false || !schemas.includes(field.schema)) continue
    const composite = compositeByID.get(field.id) ?? { schema: field.schema, name: field.name, fields: [] }
    if (field.field !== null) composite.fields.push(field)
    compositeByID.set(field.id, composite)
  }
  const compositeRef = c => `Database[${JSON.stringify(c.schema)}]["CompositeTypes"][${JSON.stringify(c.name)}]`
  const enums = {}
  const seen = new Set()
  function tsType(id, depth = 0) {
    const t = byID.get(id)
    if (!t || t.accessible === false || depth > 12) return 'unknown'
    if (t.kind === 'd') return tsType(t.base, depth + 1)
    if (t.kind === 'c') return compositeByID.has(id) ? compositeRef(compositeByID.get(id)) : 'unknown'
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
    if (seen.size + compositeByID.size > limits.relations) throw new Error('Too many exposed relations or composite types')
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
  const supportCache = new Map()
  function supported(id, depth = 0) {
    const type = byID.get(id)
    if (!type || type.accessible === false || depth > 12) return false
    const key = `${id}:${depth}`
    if (supportCache.has(key)) return supportCache.get(key)
    const result = type.kind === 'd' ? supported(type.base, depth + 1)
      : type.kind === 'c' ? compositeByID.has(id) && compositeByID.get(id).fields.every(f => supported(f.type, depth + 1))
        : type.kind === 'e' ? type.labels.length > 0
          : type.element !== '0' ? supported(type.element, depth + 1) : tsType(id) !== 'unknown'
    supportCache.set(key, result)
    return result
  }
  const callable = []
  const foreignKeyNames = new Set(relationships.map(r => `${r.schema}.${r.name}`))
  const relationKinds = new Map(columns.map(c => [`${c.schema}.${c.relation}`, c.kind]))
  const rowRef = table => `Database[${JSON.stringify(table.schema)}][${JSON.stringify(table.view ? 'Views' : 'Tables')}][${JSON.stringify(table.name)}]['Row']`
  const readableRow = id => {
    const type = byID.get(id)
    const key = type && `${type.schema}.${type.name}`
    return type?.kind === 'c' && type.accessible !== false && type.selectable && ['r', 'p', 'v'].includes(relationKinds.get(key)) ? tables.get(key) : null
  }
  for (const f of functions) {
    if (Number(f.overloads) !== 1 || !/^[a-z][a-z0-9_]{0,62}$/.test(f.name) ||
        f.modes?.some(mode => mode !== 'i') || (f.names ?? []).length !== f.arguments.length || new Set(f.names ?? []).size !== f.arguments.length) continue
    const result = byID.get(f.return_type)
    const composite = result?.kind === 'c' ? compositeByID.get(f.return_type) : null
    const table = result?.kind === 'c' ? tables.get(`${result.schema}.${result.name}`) : null
    const source = f.arguments.length === 1 ? readableRow(f.arguments[0]) : null
    const computed = source && f.setof && readableRow(f.return_type) && f.schema === source.schema && f.schema === table.schema &&
      ['s', 'i'].includes(f.volatility) && f.defaults === 0 && !tables.has(`${f.schema}.${f.name}`) && !foreignKeyNames.has(`${f.schema}.${f.name}`) &&
      !source.columns.some(c => c.name === f.name) && columns.filter(c => c.schema === source.schema && c.relation === source.name).every(c => supported(c.type))
    const args = f.arguments.map((id, i) => ({ name: f.names[i], type: computed ? rowRef(source) : tsType(id), optional: i >= f.arguments.length - f.defaults }))
    if (args.some(a => !a.name) || !computed && f.arguments.some(id => !supported(id))) continue
    if (result?.kind === 'c' && !composite && (!table || !f.setof)) continue
    let returns = table ? rowRef(table) : tsType(f.return_type)
    if (result?.schema === 'pg_catalog' && result.name === 'void') returns = 'undefined'
    if (!table && returns !== 'undefined' && !supported(f.return_type)) continue
    returns = f.setof ? table || composite ? `${returns}[]` : `(${returns} | null)[]` : composite || returns === 'undefined' ? returns : `${returns} | null`
    const fn = { schema: f.schema, name: f.name, args, returns }
    if (table && f.setof && f.schema === table.schema && readableRow(f.return_type)) fn.setof = { from: computed ? source.name : '', to: table.name, one_to_one: Boolean(computed && Number(f.rows) === 1) }
    callable.push(fn)
  }
  if (compositeByID.size > limits.relations) throw new Error('Too many exposed composite types')
  const composites = [...compositeByID.values()].map(c => ({ schema: c.schema, name: c.name,
    // Composite attributes have no NOT NULL constraints. A NULL composite RPC
    // result expands to an object of nulls, including attributes using domains.
    fields: c.fields.map(f => ({ name: f.field, type: tsType(f.type), nullable: true }))
  })).sort((a, b) => `${a.schema}.${a.name}`.localeCompare(`${b.schema}.${b.name}`))
  return { version: 3, schemas: [...schemas].sort(), tables: [...tables.values()].sort((a, b) => `${a.schema}.${a.name}`.localeCompare(`${b.schema}.${b.name}`)), functions: callable.sort((a, b) => `${a.schema}.${a.name}`.localeCompare(`${b.schema}.${b.name}`)), enums: Object.fromEntries(Object.entries(enums).sort(([a], [b]) => a.localeCompare(b))), composites }
}

export async function main(env = process.env) {
  const validators = process.argv.includes('--validators')
  if (validators && process.argv.includes('--snapshot')) throw new Error('Validators and snapshots are separate outputs')
  const { snapshot, metadata } = await inspectBundle(env.DATABASE_URL, schemaNames(env.DATA_API_SCHEMAS), { validators })
  const output = process.argv.includes('--snapshot') ? JSON.stringify({ version: 1, fingerprint: fingerprint(snapshot), snapshot }) + '\n' : validators ? generateValidators(snapshot, metadata) : generate(snapshot)
  if (Buffer.byteLength(output) > limits.outputBytes) throw new Error('Generated contract exceeds output limit')
  process.stdout.write(output)
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) main().catch(() => { console.error('Data API schema generation failed'); process.exitCode = 1 })

// OpenAPI uses the same normalized contract as TypeScript generation. Keep
// PostgreSQL expressions and internal catalog identifiers out of the document.
export function contractSchema(type) {
  if (type.endsWith(' | null')) return { ...contractSchema(type.slice(0, -7)), 'x-nullable': true }
  if (type.endsWith(')[]') && type.startsWith('(')) return { type: 'array', items: contractSchema(type.slice(1, -3)) }
  const composite = /^Database\[("(?:[^"\\]|\\.)*")\]\["CompositeTypes"\]\[("(?:[^"\\]|\\.)*")\](\[\])?$/.exec(type)
  if (composite) {
    const ref = { $ref: `#/definitions/${pointer(`${JSON.parse(composite[1])}.${JSON.parse(composite[2])}.Composite`)}` }
    return composite[3] ? { type: 'array', items: ref } : ref
  }
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
  // Nested references may cross exposed schemas. Include their definitions,
  // while paths remain restricted to the selected profile.
  for (const c of snapshot.composites ?? []) document.definitions[`${c.schema}.${c.name}.Composite`] = {
    type: 'object', properties: Object.fromEntries(c.fields.map(f => [f.name, { ...contractSchema(f.type), ...(f.nullable ? { 'x-nullable': true } : {}) }])),
    ...(c.fields.length ? { required: c.fields.map(f => f.name) } : {}), additionalProperties: false,
  }
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
      const computed = snapshot.functions.filter(f => f.schema === schema && f.setof?.from === table.name)
      if (computed.length) operation['x-gregale-computed-relationships'] = computed.map(f => ({ name: f.name, target: f.setof.to, one_to_one: f.setof.one_to_one }))
    }
  }
  for (const fn of (snapshot.functions ?? []).filter(f => f.schema === schema)) {
    const operation = document.paths?.[`/rpc/${fn.name}`]?.post
    if (!operation) continue
    if (fn.setof) operation['x-gregale-result-relation'] = fn.setof
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
  document.definitions.GregaleProblem = { type: 'object', properties: { type: { type: 'string' }, title: { type: 'string' }, status: { type: 'integer' }, code: { type: 'string' }, request_id: { type: 'string', format: 'uuid' }, retry_after_seconds: { type: 'integer', minimum: 1 } }, required: ['type', 'title', 'status', 'code', 'request_id'] }
  for (const path of Object.values(document.paths ?? {})) for (const method of ['get', 'post', 'patch', 'delete']) {
    const operation = path[method]
    if (!operation) continue
    operation.responses ??= {}
    for (const status of ['401', '413', '429', '503', '504']) operation.responses[status] ??= { description: 'Gateway authentication, body limit, user rate limit, availability or timeout failure (application/problem+json). Database failures use the PostgREST error shape.', schema: ref('GregaleProblem') }
    for (const response of Object.values(operation.responses)) {
      response.headers ??= {}
      response.headers['X-Request-Id'] = { type: 'string', description: 'Server-generated UUID; correlate with data_api_request runtime logs.' }
    }
    operation.responses['429'].headers['Retry-After'] = { type: 'integer', description: 'Gateway rate-limit retry delay in seconds; does not trigger automatic replay.' }
  }
  return document
}
