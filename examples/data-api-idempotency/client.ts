import { createDataClient, dataRequest, reconcileMutation } from '@gregale/data'
import type { Database } from './database.types.js'

/** Persist key and total before the first attempt; reuse both after a timeout. */
export async function createOrder(url: string, accessToken: string, key: string, total: number, signal?: AbortSignal) {
  const db = createDataClient<Database>({ url, accessToken }).schema('api')
  return dataRequest(db.rpc('create_order_once', { request_key: key, order_total: total }), { timeoutMs: 5000, signal })
}

/** Keep this operation bound to the original API, subject and tenant. A lookup
 * never replays a write; unresolved receipts require an explicit user decision.
 */
export async function reconcileOrder(url: string, accessToken: string, operation: Readonly<{ key: string; total: number; subject: string; tenant: string }>, signal?: AbortSignal) {
  const db = createDataClient<Database>({ url, accessToken }).schema('api')
  const result = await dataRequest(db.rpc('read_order_receipt', { request_key: operation.key, order_total: operation.total }), { timeoutMs: 5000, signal })
  return reconcileMutation(result, row => row.owner === operation.subject && row.tenant === operation.tenant && row.request_key === operation.key && row.total === operation.total)
}
