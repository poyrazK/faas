import { createDataClient, type DataClientOptions } from '@gregale/data'
import type { Database } from './database.types.js'

const ensure = (value: unknown, code: string): void => { if (!value) throw new Error(code) }

export async function exerciseRPC(url: string, aliceToken: string, bobToken: string, fetchImpl: typeof globalThis.fetch, onResponse: DataClientOptions['onResponse']) {
  const alice = createDataClient<Database>({ url, accessToken: aliceToken, fetch: fetchImpl, onResponse }).schema('api')
  const bob = createDataClient<Database>({ url, accessToken: bobToken, fetch: fetchImpl, onResponse }).schema('api')
  const created = await alice.rpc('create_note', { note_body: 'atomic canary' }).retry(false)
  ensure(!created.error && created.data?.length === 1 && created.data[0].subject === 'alice', 'typed_rpc_failed')
  const id = created.data![0].id
  const hidden = await bob.from('notes').select('id').eq('id', id).retry(false)
  ensure(!hidden.error && hidden.data?.length === 0, 'rpc_cross_subject_read_allowed')
  const other = await bob.rpc('create_note', { note_body: 'other subject' }).retry(false)
  ensure(!other.error && other.data?.length === 1 && other.data[0].subject === 'bob', 'rpc_subject_not_derived')
  const privateRow = await alice.from('notes').select('id').eq('id', other.data![0].id).retry(false)
  ensure(!privateRow.error && privateRow.data?.length === 0, 'rpc_cross_subject_row_leaked')
  const deleted = await alice.from('notes').delete().eq('id', id).select('id').retry(false)
  const otherDeleted = await bob.from('notes').delete().eq('id', other.data![0].id).select('id').retry(false)
  ensure(!deleted.error && deleted.data?.length === 1 && !otherDeleted.error && otherDeleted.data?.length === 1, 'rpc_cleanup_failed')
}

function editorContract(db: ReturnType<typeof createDataClient<Database>>) {
  db.schema('api').rpc('create_note', { note_body: 'typed' })
  // @ts-expect-error Named RPC arguments retain their PostgreSQL types.
  db.schema('api').rpc('create_note', { note_body: 7 })
  // @ts-expect-error Required RPC arguments cannot be omitted.
  db.schema('api').rpc('create_note', {})
  // @ts-expect-error Unapproved functions are absent from the generated contract.
  db.schema('api').rpc('private_note', {})
}
void editorContract
