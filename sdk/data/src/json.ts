import type { PostgrestQueryBuilder, PostgrestFilterBuilder, UnstableGetResult } from '@supabase/postgrest-js'

/** JSON columns have no inferred key schema. JSON null is a valid value. */
export type Json = string | number | boolean | null | { [key: string]: Json | undefined } | Json[]
export type JsonPath = readonly [string | number, ...(string | number)[]]
export type JsonProjection<Column extends string = string, Text extends boolean = boolean> = Readonly<{
  column: Column
  path: JsonPath
  text: Text
}>

function identifier(value: string): string {
  if (!value || /["\\\u0000-\u001f\u007f]/.test(value)) throw new Error('JSON projection names must be nonempty and cannot contain quotes, backslashes or control characters')
  return `"${value}"`
}

function expression(projection: JsonProjection): string {
  if (!projection || typeof projection.column !== 'string' || typeof projection.text !== 'boolean' || !Array.isArray(projection.path) || !projection.path.length) throw new Error('Invalid JSON projection')
  return identifier(projection.column) + projection.path.map((part, i) => {
    if (typeof part !== 'string' && (typeof part !== 'number' || !Number.isSafeInteger(part))) throw new Error('JSON array indexes must be safe integers')
    const key = typeof part === 'string' ? identifier(part) : String(part)
    return `${projection.text && i === projection.path.length - 1 ? '->>' : '->'}${key}`
  }).join('')
}

function projection<Column extends string, Text extends boolean>(column: Column, path: JsonPath, text: Text): JsonProjection<Column, Text> {
  const result = Object.freeze({ column, path: Object.freeze([...path]) as JsonPath, text })
  expression(result)
  return result
}

/** Extract text; missing paths, SQL NULL and JSON null yield null. */
export function jsonText<Column extends string>(column: Column, ...path: JsonPath): JsonProjection<Column, true> {
  return projection(column, path, true)
}

/** Extract arbitrary JSON; missing paths and SQL NULL also yield null. */
export function jsonValue<Column extends string>(column: Column, ...path: JsonPath): JsonProjection<Column, false> {
  return projection(column, path, false)
}

// Infer the pinned SDK's public builders without duplicating its schema or
// select parser. Only the explicit JSON aliases change result types.
type SourceRow<Source> = Source extends PostgrestQueryBuilder<infer _C, infer _S, infer R, infer _N, infer _F> ? R['Row']
  : Source extends PostgrestFilterBuilder<infer _C, infer _S, infer R, infer _D, infer _N, infer _F, infer _M, infer _T> ? R : never
type BaseResult<Source, Base extends string> = Base extends '' ? {}
  : Source extends PostgrestQueryBuilder<infer C, infer S, infer R, infer N, infer F> ? UnstableGetResult<S, R['Row'], N, F, Base, C>
    : Source extends PostgrestFilterBuilder<infer C, infer S, infer R, infer _D, infer N, infer F, infer _M, infer _T> ? UnstableGetResult<S, R, N, F, Base, C> : never
type CheckedFields<Source, Base extends string, Fields extends Record<string, JsonProjection>> = {
  [Key in keyof Fields]: Fields[Key]['column'] extends keyof SourceRow<Source>
    ? Key extends string | number
      ? `${Key}` extends keyof BaseResult<Source, Base> ? never : Fields[Key]
      : never : never
}
type Result<Base, Fields extends Record<string, JsonProjection>> = Base extends { error: true } ? Base
  : Base & { -readonly [Key in keyof Fields as Key extends string | number ? Key : never]: Fields[Key]['text'] extends true ? string | null : Json }
type Selection<Source, Base extends string, Fields extends Record<string, JsonProjection>> =
  Source extends PostgrestQueryBuilder<infer C, infer S, infer R, infer N, infer F>
    ? PostgrestFilterBuilder<C, S, R['Row'], Result<BaseResult<Source, Base>, Fields>[], N, F, 'GET'>
    : Source extends PostgrestFilterBuilder<infer C, infer S, infer R, infer D, infer N, infer F, infer M, infer T>
      ? PostgrestFilterBuilder<C, S, R, M extends 'RPC' ? D extends unknown[] ? Result<BaseResult<Source, Base>, Fields>[] : Result<BaseResult<Source, Base>, Fields> : Result<BaseResult<Source, Base>, Fields>[], N, F, M, T>
      : never

/** Add nullable JSON aliases to a table/view, write or relation-set RPC projection.
 * Base is the usual literal PostgREST select string; use '' for JSON aliases only.
 * JSON aliases must not duplicate fields selected by Base.
 */
export function selectJson<Source extends { select(columns: string): unknown }, const Base extends string, const Fields extends Record<string, JsonProjection>>(
  source: Source,
  base: Base & (string extends Base ? never : unknown),
  fields: Fields & CheckedFields<Source, Base, Fields> & (string extends keyof Fields ? never : number extends keyof Fields ? never : unknown),
): Selection<Source, Base, Fields> {
  const aliases = Object.entries(fields).map(([alias, field]) => `${identifier(alias)}:${expression(field)}`)
  if (!aliases.length) throw new Error('At least one JSON projection is required')
  // The SDK handles URL encoding, credentials, filters, cardinality and errors.
  return source.select([...(base.trim() ? [base] : []), ...aliases].join(',')) as Selection<Source, Base, Fields>
}
