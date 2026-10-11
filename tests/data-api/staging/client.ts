import { createDataClient } from '@gregale/data'
import type { Database } from './database.types.js'

const ensure = (value: unknown, code: string): void => { if (!value) throw new Error(code) }

export async function exercise(url: string, aliceToken: string, bobToken: string, fetchImpl: typeof globalThis.fetch = globalThis.fetch) {
  const alice = createDataClient<Database>({ url, accessToken: aliceToken, fetch: fetchImpl }).schema('api')
  const bob = createDataClient<Database>({ url, accessToken: bobToken, fetch: fetchImpl }).schema('api')
  const inserted = await alice.from('notes').insert({ subject: 'alice', body: 'canary' }).select('id,body').single()
  ensure(!inserted.error && inserted.data?.body === 'canary', 'typed_insert_failed')
  const id = inserted.data!.id
  const hidden = await bob.from('notes').select('id,body').eq('id', id)
  ensure(!hidden.error && hidden.data?.length === 0, 'cross_subject_read_allowed')
  const denied = await bob.from('notes').insert({ subject: 'alice', body: 'forbidden' })
  ensure(denied.error?.code === '42501', 'cross_subject_write_allowed')
  const updated = await alice.from('notes').update({ body: 'updated' }).eq('id', id).select('id,body').single()
  ensure(!updated.error && updated.data?.body === 'updated', 'typed_update_failed')
  return id
}

// These must fail against the types exported from the deployed database.
function editorContract(db: ReturnType<typeof createDataClient<Database>>) {
  // @ts-expect-error unknown relations cannot be queried
  db.schema('api').from('missing_relation')
  // @ts-expect-error generated identity columns cannot be inserted
  db.schema('api').from('notes').insert({ id: 1, subject: 'alice', body: 'bad' })
  // @ts-expect-error body is a required insert field
  db.schema('api').from('notes').insert({ subject: 'alice' })
}
void editorContract
