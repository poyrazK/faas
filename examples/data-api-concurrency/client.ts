import { createDataClient, optimisticMutation } from '@gregale/data'
import type { Database } from './database.types.js'

export async function editDocument(url: string, accessToken: string, id: number, version: number, body: string) {
  const db = createDataClient<Database>({ url, accessToken }).schema('api')
  return optimisticMutation(db.from('documents').update({ body }).eq('id', id).eq('version', version).select('id,body,version'))
}

export async function deleteDocument(url: string, accessToken: string, id: number, version: number) {
  const db = createDataClient<Database>({ url, accessToken }).schema('api')
  return optimisticMutation(db.from('documents').delete().eq('id', id).eq('version', version).select('id'))
}
