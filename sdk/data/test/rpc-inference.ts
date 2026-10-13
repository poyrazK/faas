import { createDataClient, validatedRpc, type QueryResult, type RpcValidationError } from '../src/index.js'
import type { Database as Base } from './database.types.js'

type Database = { api: Omit<Base['api'], 'Functions'> & { Functions: {
  create_note: { Args: { body: string; tags?: string[] }; Returns: number }
  search_notes: { Args: { phrase: string }; Returns: Base['api']['Tables']['notes']['Row'][]; SetofOptions: { from: '*'; to: 'notes'; isOneToOne: false; isSetofReturn: true } }
  count_notes: { Args: Record<PropertyKey, never>; Returns: number }
} } }
const db = createDataClient<Database>({ url: 'https://data.example', accessToken: 'token' }).schema('api')
const parser = { parse(value: unknown): { body: string; tags?: string[] } {
  if (typeof value !== 'string') throw new Error()
  return { body: value }
} }
const saved = await validatedRpc('hello', parser, args => db.rpc('create_note', args).retry(false))
if (saved.outcome === 'completed') {
  const result: QueryResult<ReturnType<typeof scalarQuery>> = saved.result
  const id: number | null = saved.result.data
  // @ts-expect-error scalar RPC does not return row objects
  const row: { id: number } | null = saved.result.data
  void [id, row, result]
} else {
  const error: RpcValidationError = saved.error
  // @ts-expect-error validation failure has no database result
  saved.result
  void error
}
function scalarQuery() { return db.rpc('create_note', { body: 'hello' }) }
const found = await validatedRpc({}, { parse: () => ({ phrase: 'hello' }) }, args => db.rpc('search_notes', args).select('id,body').single())
if (found.outcome === 'completed' && found.result.data) {
  const row: { id: number; body: string } = found.result.data
  // @ts-expect-error projection excludes subject
  found.result.data.subject
  void row
}
await validatedRpc({}, { parse: () => ({}) }, args => db.rpc('count_notes', args))
// @ts-expect-error parser output must satisfy generated RPC arguments
validatedRpc({}, { parse: () => ({ body: 42 }) }, args => db.rpc('create_note', args))
// @ts-expect-error required RPC argument cannot be missing
validatedRpc({}, { parse: () => ({ tags: ['a'] }) }, args => db.rpc('create_note', args))
// @ts-expect-error unknown function remains excluded by generated schema
validatedRpc({}, parser, args => db.rpc('unapproved', args))
// @ts-expect-error async parsers are unsupported
validatedRpc({}, { parse: async () => ({ body: 'hello' }) }, args => db.rpc('create_note', args))
// @ts-expect-error argument parser must return an object
validatedRpc({}, { parse: () => 'hello' }, args => db.rpc('create_note', args))
// @ts-expect-error callback must return an awaitable query
validatedRpc({}, parser, args => args.body)
