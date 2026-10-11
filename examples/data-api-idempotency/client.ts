import { createDataClient, dataRequest } from '@gregale/data'
import type { Database } from './database.types.js'

/** Persist key and total before the first attempt; reuse both after a timeout. */
export async function createOrder(url: string, accessToken: string, key: string, total: number, signal?: AbortSignal) {
  const db = createDataClient<Database>({ url, accessToken }).schema('api')
  return dataRequest(db.rpc('create_order_once', { request_key: key, order_total: total }), { timeoutMs: 5000, signal })
}
