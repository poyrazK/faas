import { createDataClient, dataRequest, validatedRpc, validateRpcResponse, classifyDataError } from '@gregale/data'
import type { DataClientOptions } from '@gregale/data'
import type { Database } from './database.types.js'
import { orderSummaryArgs, type OrderSummaryFilters } from './order-summary.js'

export type Order = Pick<Database['api']['Tables']['team_orders']['Row'], 'id' | 'tenant_id' | 'created_by' | 'total_cents' | 'status' | 'version'>
export type Status = Order['status']
export type Membership = Pick<Database['api']['Tables']['team_members']['Row'], 'tenant_id' | 'subject' | 'role'>
type SaveArgs = NonNullable<Database['api']['Functions']['save_team_order_once']['Args']>
export type PendingSave = Readonly<{ actor: string; args: Readonly<SaveArgs> }>
export type SaveResult = { outcome: 'saved'; order: Order } | { outcome: 'failed'; message: string; uncertain: boolean } | { outcome: 'invalid'; message: string } | { outcome: 'busy' | 'pending' | 'empty' }
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const states: readonly string[] = ['draft', 'submitted', 'fulfilled']
const object = (value: unknown): Record<string, unknown> => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Expected object')
  return value as Record<string, unknown>
}
export function parseOrder(value: unknown): Order {
  const row = object(value)
  if (typeof row.id !== 'string' || !uuid.test(row.id) || typeof row.tenant_id !== 'string' || !row.tenant_id || typeof row.created_by !== 'string' || !row.created_by || !Number.isSafeInteger(row.total_cents) || (row.total_cents as number) < 0 || (row.total_cents as number) > 2147483647 || typeof row.status !== 'string' || !states.includes(row.status) || !Number.isSafeInteger(row.version) || (row.version as number) < 1) throw new Error('Invalid order')
  return { id: row.id, tenant_id: row.tenant_id, created_by: row.created_by, total_cents: row.total_cents as number, status: row.status as Status, version: row.version as number }
}
export function teamsClient(options: DataClientOptions, tenant: string) {
  if (!tenant.trim() || tenant !== tenant.trim()) throw new Error('Choose a team')
  const db = createDataClient<Database>(options).schema('api')
  return {
    db, tenant,
    summary: (filters: OrderSummaryFilters = {}) => db.rpc('summarize_team_orders', orderSummaryArgs(tenant, filters)).retry(false),
    async membership(): Promise<Membership | null> {
      const result = await dataRequest(db.from('team_members').select('tenant_id,subject,role').eq('tenant_id', tenant), { timeoutMs: 5000 })
      const checked = validateRpcResponse(result, { parse(value) {
        if (!Array.isArray(value) || value.length > 1) throw new Error()
        if (!value.length) return null
        const row = object(value[0])
        if (row.tenant_id !== tenant || typeof row.subject !== 'string' || !row.subject || !['owner', 'admin', 'member'].includes(row.role as string)) throw new Error()
        return row as unknown as Membership
      } })
      if (checked.outcome !== 'validated') throw new Error('Could not verify team access. Check your session and try again.')
      return checked.result.data
    },
    async list(status?: Status, after?: string, pageSize = 20) {
      if (status !== undefined && !states.includes(status) || after !== undefined && !uuid.test(after) || !Number.isInteger(pageSize) || pageSize < 1 || pageSize > 100) throw new Error('Invalid page options')
      let query = db.from('team_orders').select('id,tenant_id,created_by,total_cents,status,version').eq('tenant_id', tenant).order('id').limit(pageSize + 1)
      if (status) query = query.eq('status', status)
      if (after) query = query.gt('id', after)
      const result = await dataRequest(query, { timeoutMs: 5000 })
      const checked = validateRpcResponse(result, { parse(value) {
        if (!Array.isArray(value)) throw new Error()
        const rows = value.map(parseOrder)
        if (rows.some(row => row.tenant_id !== tenant)) throw new Error()
        return rows
      } })
      if (checked.outcome !== 'validated') throw new Error('Could not load orders. Check your session and try again.')
      const rows = checked.result.data.slice(0, pageSize)
      return { rows, next: checked.result.data.length > pageSize ? rows.at(-1)!.id : null }
    },
  }
}

export function createOrderForm(client: ReturnType<typeof teamsClient>, options: { createKey?: () => string; timeoutMs?: number } = {}) {
  let pending: PendingSave | null = null, busy = false
  const key = options.createKey ?? (() => crypto.randomUUID())
  const execute = async (input: unknown, recovering: boolean): Promise<SaveResult> => {
    if (busy) return { outcome: 'busy' }
    if (recovering && !pending) return { outcome: 'empty' }
    if (!recovering && pending) return { outcome: 'pending' }
    busy = true
    try {
      let args: SaveArgs
      if (recovering) args = { ...pending!.args }
      else {
        // Parse before token lookup, membership queries or mutation transport.
        const parsed = await validatedRpc(input, { parse(value) {
          const fields = object(value)
          if (typeof fields.total !== 'string' || !/^\d+$/.test(fields.total) || !Number.isSafeInteger(Number(fields.total)) || Number(fields.total) > 2147483647) throw new Error()
          if (fields.status !== 'draft') throw new Error()
          if (fields.id !== undefined && (typeof fields.id !== 'string' || !uuid.test(fields.id) || !Number.isSafeInteger(fields.version) || (fields.version as number) < 1)) throw new Error()
          const request_key = key(), order_id = fields.id as string | undefined ?? key()
          if (!uuid.test(request_key) || !uuid.test(order_id)) throw new Error()
          return { request_key, team_id: client.tenant, order_id, expected_version: fields.id ? fields.version as number : 0, order_total: Number(fields.total), order_state: fields.status as Status }
        } }, async value => value)
        if (parsed.outcome === 'validation_failed') return { outcome: 'invalid', message: 'Enter whole cents from 0 to 2147483647 for a draft. Use Submit or Fulfil for status changes.' }
        args = parsed.result
      }
      const membership = await client.membership()
      if (!membership || membership.role === 'member') return { outcome: 'failed', message: 'An owner or admin membership is required to save orders.', uncertain: pending !== null }
      if (pending && pending.actor !== membership.subject) return { outcome: 'failed', message: 'Sign in as the original user to resolve this save.', uncertain: true }
      pending ??= Object.freeze({ actor: membership.subject, args: Object.freeze({ ...args }) })
      const result = await dataRequest(client.db.rpc('save_team_order_once', args), { timeoutMs: options.timeoutMs ?? 5000 })
      const checked = validateRpcResponse(result, { parse(value) {
        if (!Array.isArray(value) || value.length !== 1) throw new Error()
        const row = parseOrder(value[0])
        if (row.id !== args.order_id || row.tenant_id !== client.tenant || row.total_cents !== args.order_total || row.status !== args.order_state || row.version !== args.expected_version! + 1) throw new Error()
        return row
      } })
      if (checked.outcome === 'validated') { pending = null; return { outcome: 'saved', order: checked.result.data } }
      // PT409 is application-owned: distinguish version conflict by its reviewed
      // fixed message. Never render arbitrary database messages/details.
      if (result.status === 409 && result.error?.code === 'PT409' && result.error.message === 'order_version_conflict') {
        pending = null; return { outcome: 'failed', message: 'The order changed or is unavailable. Reload before editing.', uncertain: false }
      }
      if (result.error?.code === 'PT409' && ['invalid_order_transition','order_not_editable','invalid_order_revision'].includes(result.error.message)) {
        if (!recovering) pending = null
        return { outcome: 'failed', message: 'Only draft totals can be edited. Use Submit or Fulfil for status changes, and reload the current order.', uncertain: pending !== null }
      }
      const category = classifyDataError(result.error, { status: result.status })?.category
      if (['authentication', 'permission_denied', 'unique_violation', 'foreign_key_violation', 'contract_mismatch'].includes(category ?? '')) {
        // Recovery failures cannot establish what the earlier attempt committed.
        if (!recovering) pending = null
        return { outcome: 'failed', message: category === 'authentication' ? 'Sign in again before saving.' : category === 'contract_mismatch' ? 'Update the application contract before saving.' : 'The save was refused. Check team access and reload the order.', uncertain: pending !== null }
      }
      return { outcome: 'failed', message: 'The save could not be confirmed. Resolve the original operation before saving again.', uncertain: true }
    } catch {
      return { outcome: 'failed', message: 'The request could not be confirmed. Check your session and try again.', uncertain: pending !== null }
    } finally { busy = false }
  }
  return { get pending() { return pending }, submit: (input: unknown) => execute(input, false), reconcile: () => execute(undefined, true) }
}
