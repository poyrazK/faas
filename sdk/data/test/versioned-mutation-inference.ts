import { classifyVersionedMutation, createDataClient, type VersionedMutationResult } from '../src/index.js'
import type { Database } from './database.types.js'
const db = createDataClient<Database>({ url: 'https://data.example', accessToken: 'token' }).schema('api')
const query = db.from('notes').select('id,body')
const response = await query.retry(false)
const result = classifyVersionedMutation(response, row => row.id === 7 && row.body === 'saved')
const typed: VersionedMutationResult<{ id: number; body: string }, NonNullable<typeof response.error>> = result
if (result.outcome === 'applied') {
  const id: number = result.row.id
  // @ts-expect-error unselected columns remain unavailable
  result.row.subject
  void id
} else if (result.outcome === 'version_conflict') {
  const code: string = result.error.code
  void code
} else if (result.outcome === 'unresolved' && result.reason === 'replay_expired') {
  const reason: 'replay_expired' = result.reason
  void reason
}
// @ts-expect-error classify a completed response, never execute a query
classifyVersionedMutation(query, () => true)
// @ts-expect-error predicates must be synchronous
classifyVersionedMutation(response, async () => true)
void typed

type Envelope = { request_key: string | null; results: unknown }
type Validated = { request_key: string; results: { index: number; operation: 'update' | 'delete' }[] }
const envelope: { data: Envelope[]; error: null; status: number } = { data: [], error: null, status: 200 }
const matches = (row: Envelope): row is Validated => typeof row.request_key === 'string' && Array.isArray(row.results)
const batch = classifyVersionedMutation(envelope, matches)
if (batch.outcome === 'applied') {
  const operation: 'update' | 'delete' = batch.row.results[0].operation
  const key: string = batch.row.request_key
  // @ts-expect-error type guards do not invent unvalidated fields
  batch.row.results[0].private
  void [operation, key]
}
