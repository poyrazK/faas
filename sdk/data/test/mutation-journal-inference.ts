import { createMutationJournal, type MutationJournalStore, type MutationJournalContext } from '../src/index.js'
const context: MutationJournalContext = { apiURL: 'https://data.example', contract: 'order_v2', subject: 'alice', tenant: 'alpha', schemaFingerprint: null }
type Operation = MutationJournalContext & { key: string; payload: { quantities: number[] } }
declare const store: MutationJournalStore
const journal = createMutationJournal({ store, context, parser: { parse: (input: unknown) => input as Operation } })
const entry = await journal.load('checkout')
if (entry) {
  type Row = { result: unknown; key: string | null }
  type Validated = { result: { order_id: string }; key: string }
  const matches = (row: Row): row is Validated => typeof row.key === 'string' && typeof row.result === 'object'
  const response: { data: Row[]; error: null; status: number } = { data: [], error: null, status: 200 }
  const recovered = await journal.recover(entry, async operation => { const count: number = operation.payload.quantities[0]; void count; return response }, matches)
  if (recovered.result.outcome === 'confirmed') {
    const id: string = recovered.result.row.result.order_id
    // @ts-expect-error unvalidated fields are unavailable
    recovered.result.row.result.secret
    void id
  }
  const written = await journal.dispatch(entry, async () => ({ value: 7 }))
  const value: number = written.response.value
  // @ts-expect-error journal entries retain their operation type
  written.entry.operation.other
  void value
}
