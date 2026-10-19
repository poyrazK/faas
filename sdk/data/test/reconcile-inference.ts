import { createDataClient, reconcileMutation, type MutationReconciliation } from '../src/index.js'
import type { Database } from './database.types.js'
const db = createDataClient<Database>({ url: 'https://data.example', accessToken: 'token' }).schema('api')
const query = db.from('notes').select('id,body')
const response = await query.retry(false)
const result = reconcileMutation(response, row => row.id === 7 && row.body === 'original')
const typed: MutationReconciliation<{ id: number; body: string }, NonNullable<typeof response.error>> = result
if (result.outcome === 'confirmed') {
  const id: number = result.row.id
  const body: string = result.row.body
  // @ts-expect-error unselected columns remain unavailable
  const subject = result.row.subject
  void [id, body, subject]
} else if (result.outcome === 'payload_conflict') {
  const code: string = result.error.code
  void code
} else if (result.reason === 'replay_expired') {
  const reason: 'replay_expired' = result.reason
  void reason
}
// @ts-expect-error lookup must complete before classification; queries are never executed
reconcileMutation(query, () => true)
// @ts-expect-error matches must return a synchronous boolean
reconcileMutation(response, async () => true)
void typed

// A full batch envelope can narrow generated JSON to validated typed results.
type Envelope = { request_key: string | null; results: unknown }
type Validated = { request_key: string; results: { index: number; note: { id: number } }[] }
const envelope: { data: Envelope[]; error: null; status: number } = { data: [], error: null, status: 200 }
const matches = (row: Envelope): row is Validated => typeof row.request_key === 'string' && Array.isArray(row.results)
const batch = reconcileMutation(envelope, matches)
if (batch.outcome === 'confirmed') {
  const id: number = batch.row.results[0].note.id
  const key: string = batch.row.request_key
  // @ts-expect-error type guards do not invent unvalidated fields
  batch.row.results[0].note.secret
  void [id, key]
}
