import { classifyDataError, createReplayKeyV2, dataRequest, reconcileMutation, validatedRpc, type DataErrorCategory } from '@gregale/data'
import type { Database } from './database.types.js'
import { noteInputFeedback, type notesClient } from './notes.js'

export type NoteFormFields = Partial<Record<'body' | 'tags', string>>
export type SavedNote = Pick<Database['api']['Tables']['notes']['Row'], 'id' | 'body' | 'priority' | 'version'>
export type NoteFormResult =
  | { outcome: 'validation_failed'; fields: NoteFormFields }
  | { outcome: 'saved'; note: SavedNote }
  | { outcome: 'failed'; message: string; uncertain: boolean }
  | { outcome: 'busy' | 'pending' | 'empty' }

type PendingNote = Readonly<{ idempotency_key: string; note_body: string; tag_names: readonly string[] }>
class FormError extends Error {
  constructor(readonly fields: NoteFormFields) { super('Check the note fields') }
}

function parseFields(input: unknown) {
  const value = input && typeof input === 'object' ? input as Record<string, unknown> : {}
  const fields: NoteFormFields = {}
  if (typeof value.body !== 'string' || !value.body.trim()) fields.body = 'Enter a note body.'
  if (typeof value.tags !== 'string') fields.tags = 'Enter tags separated by commas, or leave this blank.'
  const tags = typeof value.tags === 'string' && value.tags.trim() ? value.tags.split(',').map(tag => tag.trim()) : []
  if (tags.some(tag => !tag)) fields.tags = 'Enter a name for each tag; remove extra commas.'
  if (Object.keys(fields).length) throw new FormError(fields)
  return { note_body: (value.body as string).trim(), tag_names: tags }
}

/** Keep this controller bound to one API and signed-in identity. Reconciliation
 * only reads the retained receipt. retry() explicitly replays the same operation
 * and requires the application to review the receipt-retention contract first.
 */
export function createNoteForm(client: ReturnType<typeof notesClient>, options: { timeoutMs?: number; createKey?: () => string; replayContract?: 'v1' | 'v2' } = {}) {
  let pending: PendingNote | null = null
  let busy = false
  const subject = client.subject
  const v2 = options.replayContract === 'v2'
  const readReceipt = v2 ? client.readCreateReceiptV2 : client.readCreateReceipt
  const writeRPC = v2 ? 'create_note_with_tags_once_v2' : 'create_note_with_tags_once'
  const timeoutMs = options.timeoutMs ?? 5000
  if (!Number.isSafeInteger(timeoutMs) || timeoutMs < 1 || timeoutMs > 2147483647) throw new RangeError('Invalid note request timeout')
  const reconcile = async (): Promise<NoteFormResult> => {
    if (busy) return { outcome: 'busy' }
    if (!pending) return { outcome: 'empty' }
    busy = true
    const operation = pending
    try {
      const response = await dataRequest(readReceipt(operation.idempotency_key, operation.note_body, operation.tag_names).select('id,body,priority,version,subject'), { timeoutMs })
      const result = reconcileMutation(response, row => row.subject === subject && row.body === operation.note_body && Number.isInteger(row.id) && Number.isInteger(row.priority) && Number.isInteger(row.version))
      if (result.outcome === 'confirmed') {
        pending = null
        const { id, body, priority, version } = result.row
        return { outcome: 'saved', note: { id, body, priority, version } }
      }
      const message = result.outcome === 'payload_conflict'
        ? 'The stored receipt has a different payload. Keep the original operation and review recovery.'
        : result.reason === 'replay_expired'
          ? 'The replay window has closed. Keep the original operation and review whether the save completed.'
        : result.reason === 'not_found'
          ? 'No matching receipt is visible. The save may still be running or its receipt may have expired.'
          : 'The save receipt could not be confirmed. Restore the same user’s session and check again.'
      return { outcome: 'failed', message, uncertain: true }
    } catch {
      return { outcome: 'failed', message: 'The save receipt could not be confirmed. Restore the same user’s session and check again.', uncertain: true }
    } finally { busy = false }
  }
  const execute = async (input: unknown, recovering: boolean): Promise<NoteFormResult> => {
    if (busy) return { outcome: 'busy' }
    if (recovering && !pending) return { outcome: 'empty' }
    if (!recovering && pending) return { outcome: 'pending' }
    busy = true
    let fields: NoteFormFields = {}
    try {
      const outcome = await validatedRpc(input, { parse(value) {
        if (recovering) return { ...pending!, tag_names: [...pending!.tag_names] }
        try {
          const parsed = parseFields(value)
          try { client.validateDraft(parsed.note_body) }
          catch (error) { throw new FormError(noteInputFeedback(error) ?? { body: 'The note does not match the database input rules.' }) }
          const key = (options.createKey ?? (v2 ? createReplayKeyV2 : () => crypto.randomUUID()))()
          if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(key)) throw new Error('Invalid request key')
          return { ...parsed, idempotency_key: key }
        } catch (error) {
          if (error instanceof FormError) fields = error.fields
          throw error
        }
      } }, args => {
        if (!recovering) pending = Object.freeze({ ...args, tag_names: Object.freeze([...args.tag_names]) })
        return dataRequest(client.db.rpc(writeRPC, args).select('id,body,priority,version,subject'), { timeoutMs })
      })
      if (outcome.outcome === 'validation_failed') return { outcome: 'validation_failed', fields: Object.keys(fields).length ? fields : { body: 'Unable to prepare the note. Try again.' } }
      const result = outcome.result
      if (!result.error && result.data?.length === 1 && result.data[0].subject === subject) {
        const { id, body, priority, version } = result.data[0]
        pending = null
        return { outcome: 'saved', note: { id, body, priority, version } }
      }
      const category = classifyDataError(result.error, { status: result.status })?.category ?? 'unknown'
      const messages: Record<DataErrorCategory, string> = {
        unique_violation: 'That value already exists. Review the note and tags.',
        foreign_key_violation: 'A related item is unavailable. Review the note and tags.',
        authentication: 'Session expired or invalid. Sign in again before saving.',
        permission_denied: 'Your session cannot save this note. Check your access.',
        contract_mismatch: 'The application needs an update before saving.',
        timeout: 'The save timed out. Its outcome is uncertain.',
        cancelled: 'The save was canceled. Its outcome is uncertain.',
        unavailable: 'The service is unavailable. The save outcome is uncertain.',
        no_response: 'No response was received. The save may have completed.',
        response_too_large: 'The save response was too large. Its outcome is uncertain.',
        rate_limited: 'Too many requests. Wait before checking the save outcome.',
        concurrency_limited: 'Too many requests are still running. Wait before checking the save outcome.',
        lock_conflict: 'Another write is blocking this save. Wait before checking its outcome.',
        deadlock: 'Concurrent writes conflicted. Wait before checking the save outcome.',
        replay_expired: 'The replay window has closed. Keep the original operation and review whether the save completed.',
        unknown: 'The save could not be confirmed.',
      }
      const uncertain = recovering || ['timeout', 'cancelled', 'unavailable', 'no_response', 'response_too_large', 'rate_limited', 'concurrency_limited', 'lock_conflict', 'deadlock', 'replay_expired', 'unknown'].includes(category)
      if (!uncertain) pending = null
      return { outcome: 'failed', message: messages[category], uncertain }
    } catch {
      return { outcome: 'failed', message: 'The save could not be confirmed.', uncertain: pending !== null }
    } finally { busy = false }
  }
  return {
    get pending() { return pending },
    submit: (input: unknown) => execute(input, false),
    reconcile,
    retry: () => execute(undefined, true),
  }
}
