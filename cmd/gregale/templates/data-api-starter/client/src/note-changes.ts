// Mirrors DataAPINoteChangeFeed* in pkg/api/limits.go (ADR-1676).
export const NOTE_CHANGE_FEED_LIMITS = Object.freeze({ maxPageSize: 100, retainedEvents: 10000, maxCursorBytes: 128 })
export type NoteChangeCursor = string & { readonly __noteChangeCursor: unique symbol }
export type NoteChange = Readonly<{ sequence: string; operation: 'insert' | 'update' | 'delete'; note_id: number; version: number }>
export type NoteChangePage = Readonly<{ next_cursor: NoteChangeCursor; has_more: boolean; changes: readonly NoteChange[] }>

function record(value: unknown, keys: readonly string[]): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    && Object.keys(value).length === keys.length && keys.every(key => Object.hasOwn(value, key))
}
const position = (value: unknown): value is string => typeof value === 'string' && value.trim() === value && /^(0|[1-9][0-9]{0,18})$/.test(value) && BigInt(value) <= 9223372036854775807n
const integer = (value: unknown): value is number => typeof value === 'number' && Number.isInteger(value) && value > 0 && value <= 2147483647

/** Opaque feed-instance/subject-bound cursor; never use it as authorization. */
export function noteChangeCursor(value: unknown): NoteChangeCursor {
  if (typeof value !== 'string' || value.length > NOTE_CHANGE_FEED_LIMITS.maxCursorBytes
      || !/^v1\.[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.[0-9a-f]{64}\.(0|[1-9][0-9]{0,18})$/.test(value)
      || !position(value.split('.').at(-1))) throw new TypeError('Invalid note change cursor')
  return value as NoteChangeCursor
}

export function noteChangePageSize(value: number): number {
  if (!Number.isInteger(value) || value < 1 || value > NOTE_CHANGE_FEED_LIMITS.maxPageSize) throw new RangeError('Note change page size must be between 1 and 100')
  return value
}

export function parseNoteSyncCheckpoint(value: unknown): NoteChangeCursor {
  if (!record(value, ['cursor'])) throw new TypeError('Invalid note sync checkpoint')
  return noteChangeCursor(value.cursor)
}

/** Validate a completed RPC row before applying changes or persisting its cursor.
 * Pass the original requested cursor to check continuity and identity binding.
 */
export function parseNoteChangePage(value: unknown, afterCursor: string): NoteChangePage {
  if (!record(value, ['next_cursor', 'has_more', 'changes']) || typeof value.has_more !== 'boolean'
      || !Array.isArray(value.changes) || value.changes.length > NOTE_CHANGE_FEED_LIMITS.maxPageSize) throw new TypeError('Invalid note change page')
  const next = noteChangeCursor(value.next_cursor), nextParts = next.split('.')
  const after = noteChangeCursor(afterCursor).split('.')
  if (after.slice(0, 3).join('.') !== nextParts.slice(0, 3).join('.')) throw new TypeError('Note change cursor context changed')
  let previous = BigInt(after[3])
  const changes: NoteChange[] = []
  for (const item of value.changes) {
    if (!record(item, ['sequence', 'operation', 'note_id', 'version']) || !position(item.sequence) || item.sequence === '0'
        || !['insert', 'update', 'delete'].includes(item.operation as string) || !integer(item.note_id) || !integer(item.version)
        || BigInt(item.sequence) !== previous + 1n) throw new TypeError('Invalid note change event')
    previous = BigInt(item.sequence)
    changes.push(Object.freeze({ sequence: item.sequence, operation: item.operation as NoteChange['operation'], note_id: item.note_id, version: item.version }))
  }
  if (previous !== BigInt(nextParts[3]) || (!changes.length && value.has_more)) throw new TypeError('Invalid note change continuation')
  return Object.freeze({ next_cursor: next, has_more: value.has_more, changes: Object.freeze(changes) })
}

export function noteChangeCursorExpired(response: { status: number; error?: unknown }): boolean {
  const error = response.error as { code?: unknown; message?: unknown } | null
  return response.status === 410 && error?.code === 'PT410' && error.message === 'note_change_cursor_expired'
}
