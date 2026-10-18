import type { PostgrestFilterBuilder } from '@supabase/postgrest-js'

type Scalar = string | number | boolean | null
export type CursorOrder = Readonly<{ column: string; ascending?: boolean; nullsFirst?: boolean; unique?: boolean }>
type Row<Query> = Query extends PostgrestFilterBuilder<infer _C, infer _S, infer R, infer _D, infer _N, infer _F, infer _M, infer _T> ? R : never
export type ReadQuery<Query> = Query extends PostgrestFilterBuilder<infer _C, infer _S, infer _R, infer D, infer _N, infer _F, infer M, infer _T>
  ? M extends 'GET' ? D extends readonly unknown[] ? unknown : never : never : never
export type CheckedOrder<Query, Order extends readonly CursorOrder[]> = {
  [I in keyof Order]: Order[I]['column'] extends keyof Row<Query>
    ? Row<Query>[Order[I]['column']] extends Scalar ? Order[I] : never : never
}
export type Cursor<Order extends readonly CursorOrder[]> = { readonly [I in keyof Order]: Scalar }
type TypedCursor<Query, Order extends readonly CursorOrder[]> = {
  readonly [I in keyof Order]: Order[I]['column'] extends keyof Row<Query> ? Row<Query>[Order[I]['column']] : never
}

function checkOrder(order: readonly CursorOrder[]) {
  if (!order.length || order.at(-1)?.unique !== true) throw new Error('Cursor ordering requires a final unique tie-breaker')
  const seen = new Set<string>()
  for (const field of order) {
    if (typeof field.column !== 'string' || !/^[A-Za-z_][A-Za-z0-9_]*$/.test(field.column) || seen.has(field.column)) throw new Error('Cursor columns must be distinct simple column names')
    if ((field.ascending !== undefined && typeof field.ascending !== 'boolean') || (field.nullsFirst !== undefined && typeof field.nullsFirst !== 'boolean')) throw new Error('Invalid cursor ordering')
    seen.add(field.column)
  }
}
function literal(value: Scalar): string {
  if (typeof value === 'string') {
    if (/[\u0000-\u001f\u007f]/.test(value)) throw new Error('Cursor strings cannot contain control characters')
    return `"${value.replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`
  }
  if (typeof value === 'number' && Number.isFinite(value) && (!Number.isInteger(value) || Number.isSafeInteger(value)) || typeof value === 'boolean') return String(value)
  throw new Error('Cursor values must be finite numbers, strings, booleans or null')
}

/** Extract a cursor from a selected row. Include every ordering column in select(). */
export function cursorFrom<const Order extends readonly [CursorOrder, ...CursorOrder[]], Data extends { [Key in Order[number]['column']]: Scalar }>(
  row: Data, order: Order,
): { readonly [I in keyof Order]: Data[Order[I]['column']] } {
  checkOrder(order)
  const values = order.map(field => {
    if (!Object.hasOwn(row, field.column)) throw new Error('A cursor ordering column was not selected')
    const value = row[field.column as Order[number]['column']]
    if (value !== null) literal(value)
    return value
  })
  if (values.at(-1) === null) throw new Error('Cursor tie-breaker must be non-null')
  return Object.freeze(values) as { readonly [I in keyof Order]: Data[Order[I]['column']] }
}

/** Apply keyset pagination to a fresh selected query, preserving its projection.
 * Keep the same filters and ordering across pages.
 * before returns nearest preceding rows in reverse order; reverse data for display.
 * The final column must be unique and non-null for the filtered result; uniqueness is caller-owned.
 */
export function cursorPage<Query extends { order(column: string, options: { ascending: boolean; nullsFirst: boolean }): Query; limit(count: number): Query; or(filters: string): Query }, const Order extends readonly [CursorOrder, ...CursorOrder[]]>(
  query: Query & ReadQuery<Query>,
  options: { order: Order & CheckedOrder<Query, Order>; size: number; after?: TypedCursor<Query, Order>; before?: TypedCursor<Query, Order> },
): Query {
  const { order, size } = options
  if (options.after !== undefined && options.before !== undefined) throw new Error('Use either after or before, not both')
  const after = options.before ?? options.after
  const traversal = options.before === undefined ? order : order.map(field => ({ ...field, ascending: !(field.ascending ?? true), nullsFirst: !(field.nullsFirst ?? false) }))
  checkOrder(order)
  if (!Number.isSafeInteger(size) || size < 1) throw new Error('Cursor page size must be a positive safe integer')
  const branches: string[] = []
  const prefix: string[] = []
  if (after !== undefined) {
    if (!Array.isArray(after) || after.length !== order.length || after.at(-1) === null) throw new Error('Cursor must contain one value per ordering column and a non-null tie-breaker')
    for (let i = 0; i < order.length; i++) {
      const { column, ascending = true, nullsFirst = false } = traversal[i]
      const value = after[i] as Scalar
      const next = value === null ? nullsFirst ? `${column}.not.is.null` : undefined
        : nullsFirst ? `${column}.${ascending ? 'gt' : 'lt'}.${literal(value)}`
          : `or(${column}.${ascending ? 'gt' : 'lt'}.${literal(value)},${column}.is.null)`
      if (next) branches.push(prefix.length ? `and(${[...prefix, next].join(',')})` : next)
      prefix.push(value === null ? `${column}.is.null` : `${column}.eq.${literal(value)}`)
    }
  }
  // Validate everything before mutating the SDK builder.
  const builder: Query = query
  for (const { column, ascending = true, nullsFirst = false } of traversal) builder.order(column, { ascending, nullsFirst })
  if (after !== undefined) builder.or(branches.join(','))
  return builder.limit(size)
}
