import { createDataClient, type DataClientOptions } from '@gregale/data'
import type { Database } from './database.types.js'

/** Pass your application's session token provider; account API keys do not belong here. */
export function createAppData(options: DataClientOptions) {
  return createDataClient<Database>(options).schema('api')
}

export function listNotes(data: ReturnType<typeof createAppData>) {
  return data.from('notes').select('id,body,created_at').order('id')
}

export function addNote(data: ReturnType<typeof createAppData>, subject: string, body: string) {
  // The database checks subject against the signed-in user on every request.
  return data.from('notes').insert({ subject, body }).select('id,body').single()
}
