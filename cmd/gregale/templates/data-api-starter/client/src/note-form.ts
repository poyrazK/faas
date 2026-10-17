import { classifyDataError, dataRequest, validatedRpc, type DataErrorCategory } from '@gregale/data'
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

/** Keep this controller bound to one API and signed-in identity. Recovery is
 * explicit same-key replay under the starter's retained-receipt contract.
 */
export function createNoteForm(client: ReturnType<typeof notesClient>, options: { timeoutMs?: number; createKey?: () => string } = {}) {
  let pending: PendingNote | null = null
  let busy = false
  const timeoutMs = options.timeoutMs ?? 5000
  if (!Number.isSafeInteger(timeoutMs) || timeoutMs < 1 || timeoutMs > 2147483647) throw new RangeError('Invalid note request timeout')
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
          const key = (options.createKey ?? (() => crypto.randomUUID()))()
          if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(key)) throw new Error('Invalid request key')
          return { ...parsed, idempotency_key: key }
        } catch (error) {
          if (error instanceof FormError) fields = error.fields
          throw error
        }
      } }, args => {
        pending = Object.freeze({ ...args, tag_names: Object.freeze([...args.tag_names]) })
        return dataRequest(client.db.rpc('create_note_with_tags_once', args).select('id,body,priority,version'), { timeoutMs })
      })
      if (outcome.outcome === 'validation_failed') return { outcome: 'validation_failed', fields: Object.keys(fields).length ? fields : { body: 'Unable to prepare the note. Try again.' } }
      const result = outcome.result
      if (!result.error && result.data?.length === 1) { pending = null; return { outcome: 'saved', note: result.data[0] } }
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
        unknown: 'The save could not be confirmed.',
      }
      const uncertain = ['timeout', 'cancelled', 'unavailable', 'no_response', 'response_too_large', 'rate_limited', 'unknown'].includes(category)
      if (!uncertain) pending = null
      return { outcome: 'failed', message: messages[category], uncertain }
    } catch {
      return { outcome: 'failed', message: 'The save could not be confirmed.', uncertain: pending !== null }
    } finally { busy = false }
  }
  return {
    get pending() { return pending },
    submit: (input: unknown) => execute(input, false),
    reconcile: () => execute(undefined, true),
  }
}
