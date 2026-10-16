import { dataRequest, validatedRpc, validateRpcResponse } from '@gregale/data'
import type { Database } from './database.types.js'
import type { teamsClient } from './orders.js'
export type TeamRole = Database['api']['Enums']['team_role']
export type ManagedMember = Pick<Database['api']['Tables']['team_members']['Row'], 'tenant_id' | 'subject' | 'role' | 'revision'>
const eventFields = 'event_id,tenant_id,actor,request_key,action,target_subject,before_role,after_role,before_revision,after_revision,actor_before_revision,actor_after_revision,actor_after_role,occurred_at' as const
export type MembershipEvent = Pick<Database['api']['Tables']['team_membership_events']['Row'], 'event_id' | 'tenant_id' | 'actor' | 'request_key' | 'action' | 'target_subject' | 'before_role' | 'after_role' | 'before_revision' | 'after_revision' | 'actor_before_revision' | 'actor_after_revision' | 'actor_after_role' | 'occurred_at'>
export type AuditCursor = Readonly<{ at: string; id: string }>
type Intent = Readonly<{ key: string; target: string; revision: string | null; role: TeamRole | null; kind: 'change' | 'transfer' }>
export type PendingMembership = Readonly<{ actor: string; intent: Intent }>
export type AdminResult = { outcome: 'changed'; event: MembershipEvent } | { outcome: 'invalid' | 'failed'; message: string; uncertain: boolean } | { outcome: 'busy' | 'pending' | 'empty' }
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const timestamp = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,6})?(?:Z|[+-]\d{2}:\d{2})$/
const role = (value: unknown): value is TeamRole => ['owner', 'admin', 'member'].includes(value as string)
const id = (value: unknown): value is string => typeof value === 'string' && uuid.test(value)
const subject = (value: unknown): value is string => typeof value === 'string' && !!value && value.length <= 256 && value === value.trim()
const object = (value: unknown) => { if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(); return value as Record<string, unknown> }
export function parseMember(value: unknown): ManagedMember {
  const row = object(value)
  if (typeof row.tenant_id !== 'string' || !row.tenant_id || !subject(row.subject) || !role(row.role) || !id(row.revision)) throw new Error()
  return { tenant_id: row.tenant_id, subject: row.subject, role: row.role, revision: row.revision }
}
export function parseMembershipEvent(value: unknown): MembershipEvent {
  const row = object(value)
  if (!id(row.event_id) || !id(row.request_key) || typeof row.tenant_id !== 'string' || !row.tenant_id || !subject(row.actor) || !subject(row.target_subject) || !['add', 'change', 'remove', 'transfer'].includes(row.action as string) || ![row.before_role, row.after_role, row.actor_after_role].every(value => value === null || role(value)) || ![row.before_revision, row.after_revision, row.actor_after_revision].every(value => value === null || id(value)) || !id(row.actor_before_revision) || typeof row.occurred_at !== 'string' || !timestamp.test(row.occurred_at) || !Number.isFinite(Date.parse(row.occurred_at))) throw new Error()
  // Exclude payload and any unexpected fields from UI/audit consumers.
  return Object.fromEntries(eventFields.split(',').map(key => [key, row[key]])) as MembershipEvent
}
export function teamAdministration(client: ReturnType<typeof teamsClient>) {
  return {
    async members(after = '', size = 20) {
      if (typeof after !== 'string' || after.length > 256 || !Number.isInteger(size) || size < 1 || size > 100) throw new Error('Invalid member page')
      const result = await dataRequest(client.db.rpc('list_team_members', { team_id: client.tenant, after_subject: after, page_size: size }).select('tenant_id,subject,role,revision'), { timeoutMs: 5000 })
      const checked = validateRpcResponse(result, { parse(value) { if (!Array.isArray(value)) throw new Error(); return value.map(parseMember) } })
      if (checked.outcome !== 'validated' || checked.result.data.some(row => row.tenant_id !== client.tenant)) throw new Error('Only team owners can view and manage membership. Reconnect to check your current access.')
      const rows = checked.result.data.slice(0, size)
      return { rows, next: checked.result.data.length > size ? rows.at(-1)!.subject : null }
    },
    async history(after?: AuditCursor, size = 20) {
      if (!Number.isInteger(size) || size < 1 || size > 100 || after && (!id(after.id) || !timestamp.test(after.at) || !Number.isFinite(Date.parse(after.at)))) throw new Error('Invalid audit page')
      const membership = await client.membership()
      if (membership?.role !== 'owner') throw new Error('Only team owners can view audit history. Reconnect to check your current access.')
      let query = client.db.from('team_membership_events').select(eventFields).eq('tenant_id', client.tenant).order('occurred_at', { ascending: false }).order('event_id', { ascending: false }).limit(size + 1)
      if (after) query = query.or(`occurred_at.lt.${after.at},and(occurred_at.eq.${after.at},event_id.lt.${after.id})`)
      const result = await dataRequest(query, { timeoutMs: 5000 })
      const checked = validateRpcResponse(result, { parse(value) { if (!Array.isArray(value)) throw new Error(); return value.map(parseMembershipEvent) } })
      if (checked.outcome !== 'validated' || checked.result.data.some(row => row.tenant_id !== client.tenant)) throw new Error('Could not load audit history. Check your session and team access.')
      const rows = checked.result.data.slice(0, size), last = rows.at(-1)
      return { rows, next: checked.result.data.length > size && last ? { at: last.occurred_at, id: last.event_id } : null }
    },
  }
}

/** Retains one immutable operation; recovery reads its receipt before attempting
 * another write. An ownership transfer can revoke the original actor's authority.
 */
export function createMembershipForm(client: ReturnType<typeof teamsClient>, options: { createKey?: () => string; timeoutMs?: number } = {}) {
  let pending: PendingMembership | null = null, busy = false
  const timeoutMs = options.timeoutMs ?? 5000
  if (!Number.isSafeInteger(timeoutMs) || timeoutMs < 1 || timeoutMs > 2147483647) throw new RangeError('Invalid administration timeout')
  const checkedEvent = (value: unknown, operation: PendingMembership) => {
    if (!Array.isArray(value) || value.length !== 1) throw new Error()
    const event = parseMembershipEvent(value[0]), intent = operation.intent
    const action = intent.kind === 'transfer' ? 'transfer' : intent.revision === null ? 'add' : intent.role === null ? 'remove' : 'change'
    if (event.tenant_id !== client.tenant || event.actor !== operation.actor || event.request_key !== intent.key || event.target_subject !== intent.target || event.before_revision !== intent.revision || event.after_role !== intent.role || event.action !== action || event.actor_after_role !== (intent.kind === 'transfer' ? 'admin' : intent.target === operation.actor ? intent.role : 'owner') || (event.actor_after_role === null ? event.actor_after_revision !== null : event.actor_after_revision === null) || (intent.role === null ? event.after_revision !== null : event.after_revision === null) || intent.kind === 'transfer' && event.actor_after_role !== 'admin') throw new Error()
    return event
  }
  const execute = async (input: unknown, recovering: boolean): Promise<AdminResult> => {
    if (busy) return { outcome: 'busy' }
    if (recovering && !pending) return { outcome: 'empty' }
    if (!recovering && pending) return { outcome: 'pending' }
    busy = true
    try {
      let intent: Intent
      if (recovering) {
        intent = pending!.intent
        const receipt = await dataRequest(client.db.rpc('get_team_change_receipt', { team_id: client.tenant, operation_key: intent.key }).select(eventFields), { timeoutMs })
        const checked = validateRpcResponse(receipt, { parse(value) { if (Array.isArray(value) && !value.length) return null; return checkedEvent(value, pending!) } })
        if (checked.outcome !== 'validated') return { outcome: 'failed', message: 'Could not verify the original change. Keep this operation pending and check your session.', uncertain: true }
        if (checked.result.data) { const event = checked.result.data; pending = null; return { outcome: 'changed', event } }
      } else {
        const parsed = await validatedRpc(input, { parse(value) {
          const fields = object(value)
          if (!subject(fields.target) || !['change', 'transfer'].includes(fields.kind as string) || fields.revision !== null && !id(fields.revision) || fields.role !== null && !role(fields.role) || fields.kind === 'transfer' && (fields.revision === null || fields.role !== 'owner') || fields.kind === 'change' && fields.revision === null && fields.role === null) throw new Error()
          const key = (options.createKey ?? (() => crypto.randomUUID()))()
          if (!id(key)) throw new Error()
          return { key, target: fields.target, revision: fields.revision as string | null, role: fields.role as TeamRole | null, kind: fields.kind as 'change' | 'transfer' }
        } }, async value => value)
        if (parsed.outcome === 'validation_failed') return { outcome: 'invalid', message: 'Choose an existing user ID and a role, or load a current membership before removing or transferring it.', uncertain: false }
        intent = parsed.result
      }
      const member = await client.membership()
      if (pending && member?.subject !== pending.actor) return { outcome: 'failed', message: 'Use the original user to resolve this change. If access was removed, its receipt may still confirm completion.', uncertain: true }
      if (member?.role !== 'owner') return { outcome: 'failed', message: 'Only team owners can change access. Reconnect to check your current role.', uncertain: pending !== null }
      if (intent.kind === 'transfer' && intent.target === member.subject) return { outcome: 'invalid', message: 'Choose another team member for ownership transfer.', uncertain: false }
      pending ??= Object.freeze({ actor: member.subject, intent: Object.freeze({ ...intent }) })
      const args = { request_key: intent.key, team_id: client.tenant, target_subject: intent.target, expected_revision: intent.revision }
      const query = intent.kind === 'transfer' ? client.db.rpc('transfer_team_ownership_once', args) : client.db.rpc('change_team_member_once', { ...args, new_role: intent.role })
      const result = await dataRequest(query.select(eventFields), { timeoutMs })
      const checked = validateRpcResponse(result, { parse: value => checkedEvent(value, pending!) })
      if (checked.outcome === 'validated') { const event = checked.result.data; pending = null; return { outcome: 'changed', event } }
      const messages: Record<string, string> = {
        last_team_owner: 'The last owner cannot be removed or demoted. Transfer ownership first.',
        membership_revision_conflict: 'Membership changed. Reload the team before making another change.',
        unknown_team_user: 'That user ID is not registered. Use a verified identity-provider user ID.',
        membership_unchanged: 'That user already has this role. Reload the team.',
        invalid_membership_change: 'Load a current membership and choose a valid change.',
        idempotency_payload_conflict: 'This request key belongs to a different change. Review the original operation.',
      }
      const message = result.error && ['PT400', 'PT409'].includes(result.error.code) ? messages[result.error.message] : undefined
      const refused = message || result.error?.code === '23505' || result.error?.code === '23503' || result.status === 401 || result.status === 403 || result.error?.code === 'schema_contract_mismatch'
      if (refused && !recovering) pending = null
      return { outcome: 'failed', message: message ?? (['23505', '23503'].includes(result.error?.code ?? '') ? 'Membership or user registration changed. Reload the team before making another change.' : result.status === 401 ? 'Sign in again before changing team access.' : result.status === 403 ? 'Only current team owners can change access. Reconnect and check your role.' : 'The change could not be confirmed. Resolve the original operation before changing access again.'), uncertain: pending !== null }
    } catch { return { outcome: 'failed', message: 'The change could not be confirmed. Check your session and resolve the original operation if it is pending.', uncertain: pending !== null } }
    finally { busy = false }
  }
  return { get pending() { return pending }, submit: (input: unknown) => execute(input, false), reconcile: () => execute(undefined, true) }
}
