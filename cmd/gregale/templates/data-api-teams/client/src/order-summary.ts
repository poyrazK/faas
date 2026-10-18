import type { Database } from './database.types.js'

type Status = Database['api']['Enums']['order_status']
type Args = NonNullable<Database['api']['Functions']['summarize_team_orders']['Args']>
export type OrderSummaryFilters = Readonly<{ status?: Status | null; minimum?: number | null; maximum?: number | null }>
export type OrderSummaryBucket = Readonly<{ status: Status; order_count: string; total_cents: string }>
export type OrderSummary = Readonly<{ tenant_id: string; order_count: string; total_cents: string; by_status: readonly OrderSummaryBucket[] }>

const statuses = ['draft', 'submitted', 'fulfilled'] as const
const object = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === 'object' && !Array.isArray(value)
const fields = (value: Record<string, unknown>, keys: readonly string[]) => Object.keys(value).length === keys.length && keys.every(key => Object.hasOwn(value, key))
const cents = (value: unknown): value is number => typeof value === 'number' && Number.isInteger(value) && value >= 0 && value <= 2147483647

export function orderSummaryArgs(team: string, filters: OrderSummaryFilters = {}): Args {
  if (typeof team !== 'string' || !team.trim() || team !== team.trim() || !object(filters) || Object.keys(filters).some(key => !['status', 'minimum', 'maximum'].includes(key))) throw new TypeError('Invalid order summary filters')
  const { status = null, minimum = null, maximum = null } = filters
  if (!(status === null || statuses.includes(status)) || !(minimum === null || cents(minimum)) || !(maximum === null || cents(maximum)) || (minimum !== null && maximum !== null && minimum > maximum)) throw new TypeError('Invalid order summary filters')
  return { team_id: team, status_filter: status, minimum_total: minimum, maximum_total: maximum }
}

// These are PostgreSQL COUNT's int8 range and the derived maximum sum of int4
// cents, rather than JavaScript safe-integer limits. Never coerce to Number.
function totals(value: Record<string, unknown>, minimum: number, maximum: number): { count: bigint; total: bigint } {
  if (typeof value.order_count !== 'string' || !/^(0|[1-9][0-9]{0,18})$/.test(value.order_count) || value.order_count.trim() !== value.order_count ||
      typeof value.total_cents !== 'string' || !/^(0|[1-9][0-9]{0,28})$/.test(value.total_cents) || value.total_cents.trim() !== value.total_cents) throw new TypeError('Invalid order summary values')
  const count = BigInt(value.order_count), total = BigInt(value.total_cents)
  if (count > 9223372036854775807n || total < count * BigInt(minimum) || total > count * BigInt(maximum)) throw new TypeError('Inconsistent order summary values')
  return { count, total }
}

export function parseOrderSummary(value: unknown, team: string, filters: OrderSummaryFilters = {}): OrderSummary {
  const args = orderSummaryArgs(team, filters)
  if (!object(value) || !fields(value, ['tenant_id', 'order_count', 'total_cents', 'by_status']) || value.tenant_id !== team || !Array.isArray(value.by_status) || value.by_status.length !== statuses.length) throw new TypeError('Invalid order summary')
  const minimum = args.minimum_total ?? 0, maximum = args.maximum_total ?? 2147483647
  const overall = totals(value, minimum, maximum)
  let count = 0n, total = 0n
  const buckets: OrderSummaryBucket[] = []
  for (let i = 0; i < statuses.length; i++) {
    const bucket = value.by_status[i]
    if (!object(bucket) || !fields(bucket, ['status', 'order_count', 'total_cents']) || bucket.status !== statuses[i]) throw new TypeError('Invalid order summary status')
    const measured = totals(bucket, minimum, maximum)
    if (args.status_filter !== null && bucket.status !== args.status_filter && measured.count !== 0n) throw new TypeError('Order summary does not match the status filter')
    count += measured.count; total += measured.total
    buckets.push(Object.freeze({ status: bucket.status as Status, order_count: bucket.order_count as string, total_cents: bucket.total_cents as string }))
  }
  if (overall.count !== count || overall.total !== total) throw new TypeError('Order summary buckets do not match the overall totals')
  return Object.freeze({ tenant_id: team, order_count: value.order_count as string, total_cents: value.total_cents as string, by_status: Object.freeze(buckets) })
}
