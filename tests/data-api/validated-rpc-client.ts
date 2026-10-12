import { createDataClient, validatedRpc, validateRpcResponse, type DataClientOptions } from '@gregale/data'
import type { Database } from './database.types.js'

export function client(options: DataClientOptions) {
  return createDataClient<Database>(options).schema('api')
}

type Args = Database['api']['Functions']['create_note']['Args']
const noteArgs = {
  parse(input: unknown): Args {
    if (!input || typeof input !== 'object') throw new Error('private input')
    const value = input as Record<string, unknown>
    for (const key of ['note_subject', 'note_slug', 'note_body']) {
      if (typeof value[key] !== 'string' || !(value[key] as string).trim()) throw new Error('private field')
    }
    if (value.parent_id !== undefined && value.parent_id !== null &&
        (typeof value.parent_id !== 'number' || !Number.isSafeInteger(value.parent_id))) throw new Error('private parent')
    return {
      note_subject: (value.note_subject as string).trim(),
      note_slug: (value.note_slug as string).trim(),
      note_body: (value.note_body as string).trim(),
      ...(value.parent_id === undefined ? {} : { parent_id: value.parent_id as number | null }),
    }
  },
}

export const create = (db: ReturnType<typeof client>, input: unknown) =>
  validatedRpc(input, noteArgs, args => db.rpc('create_note', args).select('id,subject,slug,body,parent_id').single().retry(false))
export const list = (db: ReturnType<typeof client>) =>
  validatedRpc(undefined, { parse: () => ({}) }, args => db.rpc('list_notes', args).select('id,subject,slug').retry(false))
export const count = (db: ReturnType<typeof client>) =>
  validatedRpc(undefined, { parse: () => ({}) }, args => db.rpc('count_notes', args).retry(false))
export const forbidden = (db: ReturnType<typeof client>) =>
  validatedRpc(undefined, { parse: () => ({}) }, args => db.rpc('read_secret', args).retry(false))
export const unavailable = (db: ReturnType<typeof client>) =>
  validatedRpc(undefined, { parse: () => ({}) }, args => db.rpc('busy', args).retry(false))

export async function checkedCount(db: ReturnType<typeof client>) {
  const result = await db.rpc('count_notes', {}).retry(false)
  return validateRpcResponse(result, { parse(value: unknown): number {
    if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0) throw new Error('Invalid count')
    return value
  } })
}

export async function checkedConfig(db: ReturnType<typeof client>) {
  const result = await db.rpc('read_config', {}).retry(false)
  return validateRpcResponse(result, { parse(value: unknown): { mode: 'notes' } {
    if (!value || typeof value !== 'object' || !('mode' in value) || value.mode !== 'notes') throw new Error('Invalid config')
    return { mode: value.mode }
  } })
}

// These checks compile against the contract inspected from the live database.
async function checkInference(db: ReturnType<typeof client>) {
  const saved = await create(db, {})
  if (saved.outcome === 'completed' && saved.result.data) {
    const id: number = saved.result.data.id
    const parent: number | null = saved.result.data.parent_id
    // @ts-expect-error selected RPC row has a numeric ID
    const invalid: string = saved.result.data.id
    void [id, parent, invalid]
  }
  const rows = await list(db)
  if (rows.outcome === 'completed' && rows.result.data) {
    const subject: string = rows.result.data[0].subject
    // @ts-expect-error projection excludes body
    rows.result.data[0].body
    void subject
  }
  const scalar = await count(db)
  if (scalar.outcome === 'completed') {
    const value: number | null = scalar.result.data
    // @ts-expect-error scalar RPC does not return an array
    const rows: object[] | null = scalar.result.data
    void [value, rows]
  }
  const config = await checkedConfig(db)
  if (config.outcome === 'validated') {
    const mode: 'notes' = config.result.data.mode
    // @ts-expect-error parser output excludes arbitrary JSON fields
    config.result.data.privateValue
    void mode
  }
  // @ts-expect-error generated RPC args retain required fields
  validatedRpc({}, { parse: () => ({ note_subject: 'alice' }) }, args => db.rpc('create_note', args))
  // @ts-expect-error generated RPC argument value types remain authoritative
  validatedRpc({}, { parse: () => ({ note_subject: 'alice', note_slug: 's', note_body: 42 }) }, args => db.rpc('create_note', args))
  // @ts-expect-error unapproved functions stay absent from the contract
  validatedRpc({}, { parse: () => ({}) }, args => db.rpc('unapproved', args))
}
void checkInference
