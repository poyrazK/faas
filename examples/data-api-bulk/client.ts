import { createDataClient, bulkMutations, type BulkOptions } from '@gregale/data'
import type { Database } from './database.types.js'

export async function bulkEditDocuments(
  url: string, accessToken: string,
  edits: readonly { id: number; version: number; body: string }[],
  options: BulkOptions = {},
) {
  const db = createDataClient<Database>({ url, accessToken }).schema('api')
  return bulkMutations(edits, edit => db.from('documents').update({ body: edit.body })
    .eq('id', edit.id).eq('version', edit.version).select('id,body,version'), options)
}
