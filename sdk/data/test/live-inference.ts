import { createDataClient } from '../src/index.js'
import { LiveConnection, liveRead, liveSocket, type LiveReadRow, type LiveSocket } from '../src/live/index.js'
import type { Database } from './database.types.js'

declare const socket: LiveSocket
const client = createDataClient<Database>({ url: 'https://data.example', accessToken: async () => 'jwt' }).schema('api')
const connection = new LiveConnection({ connect: () => socket })

// A live read takes its row type from the generated contract's query.
const notes = connection.query({ database: 'db', token: () => 'jwt', ...liveRead(client.from('notes').select('id,body,state,tags')) })
const row = notes.getSnapshot().rows[0]
if (row) {
  const id: number = row.id
  const body: string = row.body
  const state: 'open' | 'closed' | null = row.state
  const tags: (string | null)[] = row.tags
  // @ts-expect-error Live rows keep the generated column types.
  const wrong: string = row.id
  // @ts-expect-error Columns outside the select are absent.
  void row.payload
  void [id, body, state, tags, wrong]
}

// Anything that is not a postgrest-js query keeps plain records.
const untyped: LiveReadRow<string> = { anything: 1 }
void untyped

// liveSocket builds the connect option from the endpoint URL.
new LiveConnection({ connect: liveSocket('wss://data.shop.example/__gregale/realtime/rt-1') })

// One credential per live read: a token, anonymous access or a server key.
connection.query({ database: 'db', read: 'products', anonymous: true })
connection.query({ database: 'db', read: 'orders', serverKey: 'gdsk_x', subject: 'user-7' })
// @ts-expect-error A read is anonymous or signed in, not both.
connection.query({ database: 'db', read: 'products', anonymous: true, token: () => 'jwt' })
// @ts-expect-error A subject needs a server key.
connection.query({ database: 'db', read: 'products', token: () => 'jwt', subject: 'user-7' })
// @ts-expect-error A read needs a credential.
connection.query({ database: 'db', read: 'products' })
