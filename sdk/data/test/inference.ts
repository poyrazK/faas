import { createDataClient, type QueryData, type QueryResult, type QueryError } from '../src/index.js'
import type { Database, Tables, TablesInsert, TablesUpdate, Enums } from './database.types.js'
import { schemaFingerprint } from './database.types.js'

const client = createDataClient<Database>({ url:'https://data.example', accessToken:async ()=>'jwt', expectedSchemaFingerprint: schemaFingerprint }).schema('api')
client.from('notes').insert({subject:'alice',body:'hello',state:'open'})
client.from('notes').insert({subject:'alice',body:'hello',tags:[null,'example'],payload:{valid:true}})
// @ts-expect-error PostgreSQL text arrays cannot contain numbers.
client.from('notes').insert({subject:'alice',body:'hello',tags:[1]})
client.from('comments').insert({id:1,note_id:7,body:'reply'})
// @ts-expect-error A NOT NULL domain is required even when the attribute is nullable.
client.from('comments').insert({id:1,note_id:7})
client.from('note_bodies').select('id,body')
// @ts-expect-error V1 exports views as read contracts.
client.from('note_bodies').insert({id:1,body:'hello'})
// @ts-expect-error Required body cannot be omitted.
client.from('notes').insert({subject:'alice'})
// @ts-expect-error Enum values come from the database.
client.from('notes').insert({subject:'alice',body:'hello',state:'invalid'})
// @ts-expect-error Generated identity columns are not writable.
client.from('notes').insert({id:42,subject:'alice',body:'hello'})
// @ts-expect-error Relations outside the exported schema do not exist.
client.from('private_notes')
// @ts-expect-error Unapproved RPC functions are absent from the generated contract.
client.rpc('echo', {value:'hello'})
const result = await client.from('notes').select('id,body,state').single()
if (result.data) {
  const id: number = result.data.id
  const body: string = result.data.body
  const state: 'open' | 'closed' | null = result.data.state
  // @ts-expect-error Projections retain field types.
  const wrong: number = result.data.body
  void [id,body,state,wrong]
}
const joined = await client.from('notes').select('id,comments(id,body)').single()
if (joined.data) {
  const replies: { id: number; body: string }[] = joined.data.comments
  // @ts-expect-error reverse one-to-many relationships return arrays
  const reply: { body: string } = joined.data.comments
  // @ts-expect-error nested projections cannot invent unselected fields
  const parentID = joined.data.comments[0].note_id
  void [replies, reply, parentID]
}
const parent = await client.from('comments').select('id,notes(id,body)').single()
if (parent.data) {
  const note: { id: number; body: string } = parent.data.notes
  // @ts-expect-error many-to-one relationships return an object, not an array
  const array: { id: number; body: string }[] = parent.data.notes
  void [note, array]
}

createDataClient<Database>({ url: 'https://data.example', accessToken: 'jwt', onResponse: info => {
  const id: string | null = info.requestId
  const status: number = info.status
  const duration: number = info.durationMs
  // @ts-expect-error Response diagnostics are immutable.
  info.status = 0
  // @ts-expect-error Tokens and application metadata are not diagnostics.
  info.accessToken
  // @ts-expect-error Raw responses and bodies are not exposed.
  info.body
  void [id, status, duration]
} })
createDataClient<Database>({ url: 'https://data.example', accessToken: 'jwt', onResponse: async () => {} })

const query = client.from('notes').select('id,comments(id,body)')
type JoinedRows = QueryData<typeof query>
const rows: JoinedRows = [{ id: 1, comments: [{ id: 2, body: 'reply' }] }]
// @ts-expect-error Query helpers preserve selected column types.
const wrongRows: JoinedRows = [{ id: 'wrong', comments: [] }]
const response: QueryResult<typeof query> = await query
const queryError: QueryError<typeof query> | null = response.error
void [rows, wrongRows, queryError]

const input: TablesInsert<"notes"> = {subject:"alice",body:"hello"};
const update: TablesUpdate<"notes"> = {body:"edited"};
const state: Enums<"state"> = "open";
const view: Tables<"note_bodies"> = {id:null,body:null};
// @ts-expect-error Generated IDs remain read only in helper types.
const wrongInput: TablesInsert<"notes"> = {id:42,subject:"alice",body:"hello"};
void [input,update,state,view,wrongInput];

import { cursorPage, cursorFrom } from '../src/index.js'
const cursorOrder = [{ column: 'body' }, { column: 'id', unique: true }] as const
const previous = await cursorPage(client.from('notes').select('id,body'), { order: cursorOrder, size: 10, before: cursorFrom({ body: 'hello', id: 42 }, cursorOrder) })
if (previous.data) { const body: string = previous.data[0].body; void body }
// @ts-expect-error Backward cursor values must match their column types.
cursorPage(client.from('notes').select('id,body'), { order: cursorOrder, size: 10, before: ['hello', 'wrong'] })
// @ts-expect-error Cursor columns must exist in the relation.
cursorPage(client.from('notes').select(), { order: [{ column: 'missing', unique: true }], size: 10, before: ['hello'] })

import { dataRequest } from '../src/index.js'
const bounded = await dataRequest(client.from('notes').select('id,body'), { timeoutMs: 5000 })
if (bounded.data) { const body: string = bounded.data[0].body; void body }
// @ts-expect-error Deadlines preserve projection types.
const wrongBounded: { id: string }[] | null = bounded.data
void wrongBounded

import { optimisticMutation } from '../src/index.js'
const optimistic = await optimisticMutation(client.from('notes').update({ body: 'edited' }).eq('id', 42).select('id,body'))
if (optimistic.outcome === 'applied') {
  const body: string = optimistic.row.body
  // @ts-expect-error Conditional mutations preserve selected field types.
  const wrong: number = optimistic.row.body
  void [body,wrong]
}
// @ts-expect-error Mutation helpers require a selected array of rows.
optimisticMutation(client.from('notes').update({body:'edited'}))

import { bulkMutations } from '../src/index.js'
const bulk = await bulkMutations([{id:42,body:'edited'}], item => client.from('notes').update({body:item.body}).eq('id',item.id).select('id,body'))
if (bulk[0].outcome === 'applied') {
  const body: string = bulk[0].rows[0].body
  // @ts-expect-error Bulk results preserve selected field types.
  const wrong: number = bulk[0].rows[0].body
  void [body,wrong]
}
// @ts-expect-error Bulk mutations require selected rows.
bulkMutations([1], id => client.from('notes').delete().eq('id',id))
