import { createDataClient, type DataClientOptions } from '@gregale/data'
import * as contract from './database.types.js'

const numericMode = (contract as typeof contract & { dataAPINumericMode?: DataClientOptions['numericMode'] }).dataAPINumericMode ?? 'number'

/** Pass your application's session token provider; account API keys do not belong here. */
export function createAppData(options: DataClientOptions) {
  return createDataClient<contract.Database>({ ...options, numericMode }).schema('api')
}

export function listNotes(data: ReturnType<typeof createAppData>) {
  return data.from('notes').select('id,body,created_at').order('id')
}

export function addNote(data: ReturnType<typeof createAppData>, subject: string, body: string) {
  // The database checks subject against the signed-in user on every request.
  return data.from('notes').insert({ subject, body }).select('id,body').single()
}
