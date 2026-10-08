import { createDataClient } from '@gregale/data'
import type { Database } from './database.types.js'

export function notesClient(options: {
  url: string
  subject: string
  accessToken: string | (() => string | Promise<string>)
  fetch?: typeof globalThis.fetch
}) {
  const db = createDataClient<Database>(options).schema('api')
  return {
    list: () => db.from('notes').select('id,body,priority,created_at').order('created_at', { ascending: false }).range(0, 19),
    listWithReplies: () => db.from('notes').select('id,body,comments(id,body),note_details(summary)').order('created_at', { ascending: false }).range(0, 19),
    listWithTags: () => db.from('notes').select('id,body,tags!note_tags(id,name)').order('id').range(0, 19),
    listWithFavoriteTags: () => db.from('notes').select('id,body,tags!note_favorite_tags(id,name)').order('id').range(0, 19),
    listFavoriteTaggedNotes: () => db.from('tags').select('id,name,notes!note_favorite_tags(id,body)').order('id').range(0, 19),
    listTaggedNotes: () => db.from('tags').select('id,name,notes!note_tags(id,body)').order('id').range(0, 19),
    listComments: () => db.from('comments').select('id,body,notes(id,body)').order('id').range(0, 19),
    reply: (note_id: number, body: string) => db.from('comments').insert({ subject: options.subject, note_id, body }).select().single(),
    create: (body: string, priority = 0) => db.from('notes').insert({ subject: options.subject, body, priority }).select().single(),
    update: (id: number, patch: { body?: string; priority?: number }) => db.from('notes').update(patch).eq('id', id).select().single(),
    remove: (id: number) => db.from('notes').delete().eq('id', id),
    // Exposed for application-specific filters and projections with the same types.
    db,
  }
}
