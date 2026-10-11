import { createDataClient } from '../src/index.js'
import type { Database } from './database.types.js'

const client = createDataClient<Database>({ url:'https://data.example', accessToken:async ()=>'jwt' }).schema('api')
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
// @ts-expect-error RPC is excluded from the V1 Data API contract.
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
