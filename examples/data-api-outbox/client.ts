import { createDataClient, dataRequest } from '@gregale/data'
import type { Database } from './database.types.js'

/** Persist key and total before the first attempt; reuse both after a timeout. */
export async function createOrder(url: string, accessToken: string, key: string, total: number, signal?: AbortSignal) {
  const db = createDataClient<Database>({ url, accessToken }).schema('api')
  return dataRequest(db.rpc('create_order_once', { request_key: key, order_total: total }), { timeoutMs: 5000, signal })
}

/** Run using an issuer-signed worker token for one subject and tenant. */
export async function dispatchOne(
  url: string, workerToken: string,
  deliver: (eventKey: string, order: { id: number; total: number }) => Promise<void>,
) {
  const db = createDataClient<Database>({ url, accessToken: workerToken }).schema('api')
  const token = crypto.randomUUID()
  const claimed = await dataRequest(db.rpc('claim_event', { claim_token: token }), { timeoutMs: 5000 })
  if (claimed.error) throw claimed.error
  const event = claimed.data?.[0]
  if (!event) return 'idle' as const
  const order = await dataRequest(db.from('orders').select('id,total').eq('id', event.order_id).limit(1), { timeoutMs: 5000 })
  if (order.error) throw order.error
  let succeeded = false
  try {
    // Consumers must deduplicate this stable identity across lease retries.
    if (!order.data?.[0]) throw new Error('Outbox order is unavailable')
    await deliver(`order-created:${event.id}`, order.data[0])
    succeeded = true
  } catch { /* Store retry scheduling, never raw provider errors or secrets. */ }
  const finished = await dataRequest(db.rpc('finish_event', { event_id: event.id, claim_token: token, succeeded }), { timeoutMs: 5000 })
  if (finished.error) throw finished.error
  if (!finished.data?.length) return 'lease-lost' as const
  if (finished.data[0].dead_lettered) return 'dead-lettered' as const
  return succeeded ? 'delivered' as const : 'retry-scheduled' as const
}


/** Read generation from the dead-letter row before requesting an explicit replay. */
export async function replayEvent(url: string, workerToken: string, id: number, generation: number) {
  const db = createDataClient<Database>({ url, accessToken: workerToken }).schema('api')
  return dataRequest(db.rpc('replay_event', { event_id: id, expected_generation: generation }), { timeoutMs: 5000 })
}

export async function queueHealth(url: string, token: string) {
  const db = createDataClient<Database>({ url, accessToken: token }).schema('api')
  return dataRequest(db.from('outbox_health').select('pending,leased,delivered,dead_lettered'), { timeoutMs: 5000 })
}

/** Configure a visible event's budget with a trusted worker credential. */
export async function setRetryLimit(url: string, workerToken: string, id: number, limit: number) {
  if (!Number.isSafeInteger(limit) || limit < 1 || limit > 1000) throw new Error('Retry limit must be between 1 and 1000')
  const db = createDataClient<Database>({ url, accessToken: workerToken }).schema('api')
  return dataRequest(db.from('outbox').update({ max_attempts: limit }).eq('id', id).select('id,max_attempts'), { timeoutMs: 5000 })
}
