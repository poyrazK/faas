import { createDataClient, ExactNumber } from '@gregale/data'
import { exercise as exerciseCRUD } from './client.js'
import { dataAPINumericMode, type Database } from './database.types.js'

export function exercise(url: string, aliceToken: string, bobToken: string, fetchImpl: typeof globalThis.fetch) {
  return exerciseCRUD(url, aliceToken, bobToken, fetchImpl, dataAPINumericMode)
}

export async function exerciseExact(url: string, token: string, id: ExactNumber, fetchImpl: typeof globalThis.fetch) {
  const alice = createDataClient<Database>({ url, accessToken: token, fetch: fetchImpl, numericMode: dataAPINumericMode }).schema('api')
  const expected = new ExactNumber('9007199254740993')
  const written = await alice.from('notes').update({ exact_value: expected }).eq('id', id).select('id').single().retry(false)
  if (written.error || !(written.data?.id instanceof ExactNumber) || written.data.id.source !== id.source) {
    throw new Error('exact_bigint_write_failed')
  }
  const read = await alice.from('notes').select('exact_value').eq('id', id).single().retry(false)
  const actual = read.data?.exact_value
  if (read.error || !(actual instanceof ExactNumber) || actual.source !== expected.source) {
    throw new Error('exact_bigint_read_failed')
  }
}
