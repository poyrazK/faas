import { createReplayKeyV2 } from '@gregale/data'
import type { Database } from './database.types.js'
import type { NotePatch } from './notes.js'

// Fixed v2 contract, mirrored from pkg/api/limits.go (ADR-1672).
export const NOTE_BATCH_V2_MAX_OPERATIONS = 100
export type NoteBatchOperation =
  | { readonly operation: 'update'; readonly note_id: number; readonly expected_version: number; readonly patch: Readonly<NotePatch> }
  | { readonly operation: 'delete'; readonly note_id: number; readonly expected_version: number }
export type PendingNoteBatch = Readonly<{ subject: string; key: string; operations: readonly NoteBatchOperation[] }>
type BatchRow = Database['api']['CompositeTypes']['note_batch_result_v2']
type NoteSnapshot = Pick<Database['api']['Tables']['notes']['Row'], 'id' | 'subject' | 'body' | 'priority' | 'version' | 'created_at'>
export type NoteBatchReceipt = { request_key: string; subject: string; results: { index: number; operation: 'update' | 'delete'; note: NoteSnapshot }[] }

const object = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === 'object' && !Array.isArray(value)
const integer = (value: unknown, min = 1): value is number => typeof value === 'number' && Number.isInteger(value) && value >= min && value <= 2147483647
const keyPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i

/** Capture once, before the request. Retain this object and its API/identity
 * context until the application resolves an uncertain outcome; never replace
 * an expired key automatically. The database clock decides key validity.
 */
export function prepareNoteBatch(subject: string, operations: readonly NoteBatchOperation[], key?: string): PendingNoteBatch {
  const invalid = () => { throw new TypeError('A batch requires distinct note IDs, positive integer versions and supported update/delete operations') }
  if (typeof subject !== 'string' || !subject || !Array.isArray(operations) || operations.length < 1 || operations.length > NOTE_BATCH_V2_MAX_OPERATIONS) invalid()
  const ids = new Set<number>()
  const captured = Array.from(operations, (item): NoteBatchOperation => {
    if (!object(item) || !integer(item.note_id) || !integer(item.expected_version) || ids.has(item.note_id) ||
        Object.keys(item).some(key => !['operation', 'note_id', 'expected_version', 'patch'].includes(key))) return invalid()
    ids.add(item.note_id)
    if (item.operation === 'delete' && !Object.hasOwn(item, 'patch')) return Object.freeze({ operation: 'delete', note_id: item.note_id, expected_version: item.expected_version })
    if (item.operation !== 'update' || !object(item.patch) || !Object.keys(item.patch).length ||
        Object.keys(item.patch).some(key => !['body', 'priority'].includes(key)) ||
        (Object.hasOwn(item.patch, 'body') && typeof item.patch.body !== 'string') ||
        (Object.hasOwn(item.patch, 'priority') && !integer(item.patch.priority, -2147483648))) return invalid()
    return Object.freeze({ operation: 'update', note_id: item.note_id, expected_version: item.expected_version, patch: Object.freeze({ ...item.patch }) })
  })
  if (key !== undefined && (typeof key !== 'string' || !keyPattern.test(key))) throw new TypeError('A canonical UUIDv7 batch key is required')
  return Object.freeze({ subject, key: (key ?? createReplayKeyV2()).toLowerCase(), operations: Object.freeze(captured) })
}

/** Runtime validation narrows generated JSON only after the entire original
 * batch matches. Timestamps remain strings so PostgreSQL precision is retained.
 */
export function noteBatchMatches(pending: PendingNoteBatch): (row: BatchRow) => row is NoteBatchReceipt {
  return (row): row is NoteBatchReceipt => {
    if (!object(row) || row.request_key !== pending.key || row.subject !== pending.subject || !Array.isArray(row.results) ||
        row.results.length !== pending.operations.length || row.results.length < 1 || row.results.length > NOTE_BATCH_V2_MAX_OPERATIONS) return false
    return Array.from(row.results).every((entry, index) => {
      const operation = pending.operations[index]
      if (!object(entry) || entry.index !== index || entry.operation !== operation.operation || !object(entry.note)) return false
      const note = entry.note
      if (note.id !== operation.note_id || note.subject !== pending.subject ||
          note.version !== operation.expected_version + (operation.operation === 'update' ? 1 : 0) || !integer(note.version) ||
          typeof note.body !== 'string' || !integer(note.priority, -2147483648) ||
          typeof note.created_at !== 'string' || !/^\d{4}-\d{2}-\d{2}T/.test(note.created_at) || !Number.isFinite(Date.parse(note.created_at))) return false
      return operation.operation === 'delete' || Object.entries(operation.patch).every(([field, value]) => note[field] === value)
    })
  }
}
