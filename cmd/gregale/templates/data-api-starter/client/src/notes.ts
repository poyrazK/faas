import { createDataClient } from '@gregale/data'
import type { Database } from './database.types.js'

type NoteBatchItem = Pick<Database['api']['Tables']['notes']['Insert'], 'body' | 'priority'>
type DetailBatchItem = Pick<Database['api']['Tables']['note_details']['Insert'], 'note_id' | 'summary'>

export type NoteCursor = Pick<Database['api']['Tables']['notes']['Row'], 'created_at' | 'id'>

// Keep fractional seconds intact: Date.toISOString() would lose PG microseconds.
export function noteCursor(row: NoteCursor): NoteCursor {
  const stamp = row?.created_at
  const match = typeof stamp === 'string' && /^(\d{4})-(\d{2})-(\d{2})T([01]\d|2[0-3]):[0-5]\d:[0-5]\d(?:\.\d{1,6})?(?:Z|[+-](?:0\d|1[0-4]):[0-5]\d)$/.exec(stamp)
  const year = match ? Number(match[1]) : 0
  const month = match ? Number(match[2]) : 0
  const days = [31, year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0) ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31]
  if (!match || !Number.isFinite(Date.parse(stamp)) || Number(match[3]) < 1 || Number(match[2]) < 1 || Number(match[2]) > 12 ||
      year < 1 || Number(match[3]) > days[month - 1] ||
      !Number.isInteger(row.id) || row.id < 1 || row.id > 2147483647) {
    throw new TypeError('Cursor requires a valid ISO timestamp and a positive PostgreSQL integer id')
  }
  return { created_at: stamp, id: row.id }
}

export function notesClient(options: {
  url: string
  subject: string
  accessToken: string | (() => string | Promise<string>)
  fetch?: typeof globalThis.fetch
}) {
  const db = createDataClient<Database>(options).schema('api')
  return {
    cursorPage: ({ after, size = 20, priority }: { after?: NoteCursor; size?: number; priority?: number } = {}) => {
      if (!Number.isSafeInteger(size) || size < 1) throw new RangeError('Cursor page size must be a positive safe integer')
      const cursor = after === undefined ? undefined : noteCursor(after)
      let query = db.from('notes').select('id,body,priority,created_at')
        .order('created_at', { ascending: false }).order('id', { ascending: false }).limit(size)
      if (cursor) query = query.or(`created_at.lt.${cursor.created_at},and(created_at.eq.${cursor.created_at},id.lt.${cursor.id})`)
      return priority === undefined ? query : query.eq('priority', priority)
    },
    page: ({ offset = 0, size = 20, priority }: { offset?: number; size?: number; priority?: number } = {}) => {
      if (!Number.isSafeInteger(offset) || offset < 0 || !Number.isSafeInteger(size) || size < 1 || size - 1 > Number.MAX_SAFE_INTEGER - offset) {
        throw new RangeError('Pagination requires a nonnegative safe offset and a positive safe size')
      }
      const query = db.from('notes').select('id,body,priority,created_at', { count: 'exact' })
        .order('created_at', { ascending: false }).order('id', { ascending: false })
        .range(offset, offset + size - 1)
      return priority === undefined ? query : query.eq('priority', priority)
    },
    list: () => db.from('notes').select('id,body,priority,created_at').order('created_at', { ascending: false }).range(0, 19),
    listWithReplies: () => db.from('notes').select('id,body,comments(id,body),note_details(summary)').order('created_at', { ascending: false }).range(0, 19),
    listWithTags: () => db.from('notes').select('id,body,tags!note_tags(id,name)').order('id').range(0, 19),
    listWithFavoriteTags: () => db.from('notes').select('id,body,tags!note_favorite_tags(id,name)').order('id').range(0, 19),
    listFavoriteTaggedNotes: () => db.from('tags').select('id,name,notes!note_favorite_tags(id,body)').order('id').range(0, 19),
    listTaggedNotes: () => db.from('tags').select('id,name,notes!note_tags(id,body)').order('id').range(0, 19),
    listComments: () => db.from('comments').select('id,body,notes(id,body)').order('id').range(0, 19),
    reply: (note_id: number, body: string) => db.from('comments').insert({ subject: options.subject, note_id, body }).select().single(),
    createMany: (rows: NoteBatchItem[]) => {
      if (!rows.length) throw new RangeError('Batch insert requires at least one row')
      return db.from('notes').insert(rows.map(row => ({ ...row, subject: options.subject })), { defaultToNull: false }).select().retry(false)
    },
    saveDetails: (rows: DetailBatchItem[]) => {
      if (!rows.length) throw new RangeError('Batch upsert requires at least one row')
      return db.from('note_details').upsert(rows.map(row => ({ ...row, subject: options.subject })), { onConflict: 'subject,note_id' }).select().retry(false)
    },
    create: (body: string, priority = 0) => db.from('notes').insert({ subject: options.subject, body, priority }).select().single(),
    update: (id: number, patch: { body?: string; priority?: number }) => db.from('notes').update(patch).eq('id', id).select().single(),
    remove: (id: number) => db.from('notes').delete().eq('id', id),
    // Exposed for application-specific filters and projections with the same types.
    db,
  }
}
