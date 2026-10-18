import { createDataClient, createReplayKeyV2, createMutationJournal, dataRequest, reconcileMutation, type MutationJournalEntry, type MutationJournalStore, type DataClientOptions } from '@gregale/data'
import { rpcValidators, type Database } from './database.types.js'

type Args = Database['api']['Functions']['__OP___v2']['Args']
type Row = Database['api']['CompositeTypes']['__OP___v2_result']
// SQL arguments permit null structurally; this operation's business rules do not.
type Input = { [K in 'skus' | 'quantities']: NonNullable<NonNullable<Args[K]>[number]>[] }
export type TransactionReceipt = {
  request_key: string; tenant_id: string; created_by: string; order_id: string
  total_cents: number; created_at: string
  lines: { sku: string; quantity: number; unit_price_cents: number }[]
}
export type PendingTransaction = Readonly<{
  contract: '__OP___v2'; apiURL: string; schemaFingerprint: string | null
  subject: string; tenant: string; key: string
  skus: readonly string[]; quantities: readonly number[]
}>
const uuid = /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i
const replayKey = /^[a-f0-9]{8}-[a-f0-9]{4}-7[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/i
const object = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === 'object' && !Array.isArray(value)
const integer = (value: unknown, min: number, max: number): value is number => typeof value === 'number' && Number.isSafeInteger(value) && value >= min && value <= max

function lines(input: unknown): Input {
  if (!object(input) || Object.keys(input).some(key => !['skus', 'quantities'].includes(key)) ||
      !Array.isArray(input.skus) || !Array.isArray(input.quantities) || input.skus.length < 1 || input.skus.length > 100 ||
      input.skus.length !== input.quantities.length) throw new TypeError('Invalid transaction items')
  const skus = Array.from(input.skus), quantities = Array.from(input.quantities)
  if (skus.some(sku => typeof sku !== 'string' || !/^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(sku)) ||
      new Set(skus).size !== skus.length || quantities.some(quantity => !integer(quantity, 1, 1000))) throw new TypeError('Invalid transaction items')
  // ASCII SKU order matches SQL COLLATE "C". Arrays are aligned before sorting.
  const ordered = skus.map((sku, index) => ({ sku, quantity: quantities[index] })).sort((a, b) => a.sku < b.sku ? -1 : a.sku > b.sku ? 1 : 0)
  return { skus: ordered.map(item => item.sku), quantities: ordered.map(item => item.quantity) }
}

export const transactionArguments = {
  parse(input: unknown): Args {
    const value = rpcValidators.api.__OP___v2.parse(input)
    if (typeof value.request_key !== 'string' || !replayKey.test(value.request_key)) throw new TypeError('A UUIDv7 transaction key is required')
    const canonical = lines({ skus: value.skus, quantities: value.quantities })
    return { request_key: value.request_key.toLowerCase(), ...canonical }
  },
}

/** Match the complete original receipt, including server-snapshotted prices.
 * This confirms the original transaction; it makes no claim about current stock.
 */
export function transactionMatches(pending: PendingTransaction): (row: Row) => row is TransactionReceipt {
  return (row): row is TransactionReceipt => {
    if (!object(row) || row.request_key !== pending.key || row.tenant_id !== pending.tenant || row.created_by !== pending.subject ||
        typeof row.order_id !== 'string' || !uuid.test(row.order_id) || !integer(row.total_cents, 0, 100000000000000) ||
        typeof row.created_at !== 'string' || !/^\d{4}-\d{2}-\d{2}T/.test(row.created_at) || !Number.isFinite(Date.parse(row.created_at)) ||
        !Array.isArray(row.lines) || row.lines.length !== pending.skus.length || row.lines.length < 1 || row.lines.length > 100) return false
    let total = 0
    for (const [index, item] of Array.from(row.lines).entries()) {
      if (!object(item) || item.sku !== pending.skus[index] || item.quantity !== pending.quantities[index] ||
          !integer(item.unit_price_cents, 0, 1000000000)) return false
      total += pending.quantities[index] * item.unit_price_cents
    }
    return total === row.total_cents
  }
}

export function transactionClient(options: DataClientOptions & { subject: string; tenant: string }) {
  const db = createDataClient<Database>(options).schema('api')
  // Keep the API and pinned schema contract with any saved pending operation.
  const context = Object.freeze({ contract: '__OP___v2' as const, apiURL: new URL(options.url).toString().replace(/\/$/, ''),
    schemaFingerprint: options.expectedSchemaFingerprint ?? null, subject: options.subject, tenant: options.tenant })
  if (typeof context.subject !== 'string' || !context.subject || [...context.subject].length > 256 ||
      typeof context.tenant !== 'string' || !context.tenant || [...context.tenant].length > 128) throw new TypeError('Transaction identity required')
  const args = (pending: PendingTransaction): Args => {
    if (pending.contract !== '__OP___v2' || Object.entries(context).some(([key, value]) => pending[key as keyof PendingTransaction] !== value)) throw new TypeError('Pending transaction context changed')
    return transactionArguments.parse({ request_key: pending.key, skus: [...pending.skus], quantities: [...pending.quantities] })
  }
  // Preparation is separate from persistence so callers can opt into storage.
  function prepare(input: unknown, key?: string): PendingTransaction {
    const canonical = lines(input)
    if (key !== undefined && (typeof key !== 'string' || !replayKey.test(key))) throw new TypeError('A UUIDv7 transaction key is required')
    return Object.freeze({ ...context, key: (key ?? createReplayKeyV2()).toLowerCase(),
      skus: Object.freeze([...canonical.skus]), quantities: Object.freeze([...canonical.quantities]) })
  }
  function restore(input: unknown): PendingTransaction {
    if (!object(input) || Object.keys(input).sort().join(',') !== 'apiURL,contract,key,quantities,schemaFingerprint,skus,subject,tenant' ||
        Object.entries(context).some(([field, expected]) => input[field] !== expected) || typeof input.key !== 'string') throw new TypeError('Invalid pending transaction context')
    const restored = prepare({ skus: input.skus, quantities: input.quantities }, input.key)
    if (input.key !== restored.key || JSON.stringify(input.skus) !== JSON.stringify(restored.skus) ||
        JSON.stringify(input.quantities) !== JSON.stringify(restored.quantities)) throw new TypeError('Saved transaction must retain its canonical key and items')
    return restored
  }
  return {
    context, prepare, restore,
    execute: (pending: PendingTransaction) => db.rpc('__OP___v2', args(pending)).retry(false),
    lookup: (pending: PendingTransaction) => db.rpc('read___OP___receipt_v2', args(pending)).retry(false),
  }
}
// Explicit persistence is opt-in. A stored prepared operation is atomically
// claimed before sending; reload and recover never issue a mutation request.
export function persistentTransactionClient(options: DataClientOptions & { subject: string; tenant: string }, store: MutationJournalStore) {
  const transaction = transactionClient(options)
  const journal = createMutationJournal({ store, context: transaction.context, parser: { parse: transaction.restore } })
  type Entry = MutationJournalEntry<PendingTransaction>
  type RequestOptions = Parameters<typeof dataRequest>[1]
  type Response = Awaited<ReturnType<typeof transaction.execute>>
  return {
    prepare: (slot: string, input: unknown, key?: string) => journal.prepare(slot, transaction.prepare(input, key)),
    load: journal.load,
    execute: (entry: Entry, request: RequestOptions) => journal.dispatch(entry, pending => dataRequest(transaction.execute(pending), request)),
    reconcile: (entry: Entry, response: Response) => journal.reconcile(entry, response, transactionMatches(entry.operation)),
    recover: (entry: Entry, request: RequestOptions) => journal.recover(entry, pending => dataRequest(transaction.lookup(pending), request), transactionMatches(entry.operation)),
    removeConfirmed: journal.removeConfirmed,
  }
}
export { reconcileMutation }
