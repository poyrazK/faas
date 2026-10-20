import { createDataClient } from '../src/index.js'
import type { Database as Base } from './database.types.js'

type Database = { api: Omit<Base['api'], 'Functions'> & { Functions: {
  search_products: { Args: { q: string }; Returns: Base['api']['Tables']['notes']['Row'][]; SetofOptions: { from: '*'; to: 'notes'; isOneToOne: false; isSetofReturn: true } }
} } }
declare const session: { token: string } | null

// Public data without a session; the generated contract still types it.
const visitor = createDataClient<Database>({ url: 'https://data.example', anonymous: true }).schema('api')
const found = await visitor.rpc('search_products', { q: 'lamp' }, { get: true })
if (found.data) {
  const body: string = found.data[0].body
  void body
}
// @ts-expect-error Public RPC arguments keep their generated types.
void visitor.rpc('search_products', { q: 1 }, { get: true })
// One client for signed-in and signed-out visitors.
createDataClient<Database>({ url: 'https://data.example', anonymous: true, accessToken: () => session?.token ?? null })
// @ts-expect-error Without anonymous: true a token is required.
createDataClient<Database>({ url: 'https://data.example' })
// @ts-expect-error Without anonymous: true a token source cannot return null.
createDataClient<Database>({ url: 'https://data.example', accessToken: () => session?.token ?? null })
// @ts-expect-error anonymous: false keeps the token requirement.
createDataClient<Database>({ url: 'https://data.example', anonymous: false })
