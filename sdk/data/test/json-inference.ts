import { createDataClient, selectJson, jsonText, jsonValue, type Json, type JsonProjection, type QueryData } from '../src/index.js'
import type { Database } from './database.types.js'

const db = createDataClient<Database>({ url: 'https://data.example', accessToken: 'jwt' }).schema('api')
const query = selectJson(db.from('notes'), 'id,comments(id,body)', {
  status: jsonText('payload', 'status'),
  details: jsonValue('payload', 'details'),
  first: jsonText('payload', 'items', 0, 'name'),
}).eq('payload->>status', 'paid').order('id').limit(10)
type Rows = QueryData<typeof query>
const rows: Rows = [{ id: 1, comments: [], status: null, details: { count: 2 }, first: 'item' }]
rows[0].status = 'paid'
// @ts-expect-error Plain selected columns retain their non-null numeric type.
const wrongID: Rows = [{ id: null, comments: [], status: null, details: null, first: null }]
// @ts-expect-error JSON text extraction is text, not a JSON number.
const wrongText: Rows = [{ id: 1, comments: [], status: 42, details: null, first: null }]
const result = await query.single()
if (result.data) {
  const status: string | null = result.data.status
  const details: Json = result.data.details
  // @ts-expect-error Missing keys can return null even for NOT NULL JSON columns.
  const nonNull: string = result.data.status
  // @ts-expect-error Arbitrary JSON has no inferred object keys.
  const invented = result.data.details.count
  // @ts-expect-error Nested selections retain their projection.
  const unselected = result.data.comments[0].note_id
  void [status, details, nonNull, invented, unselected]
}
// @ts-expect-error JSON source columns must exist in the generated row contract.
selectJson(db.from('notes'), 'id', { status: jsonText('missing', 'status') })
// @ts-expect-error JSON aliases cannot replace an already selected column.
selectJson(db.from('notes'), 'id', { id: jsonText('payload', 'id') })
// @ts-expect-error A path must contain at least one key or array index.
jsonText('payload')
const jsonOnly = await selectJson(db.from('notes'), '', { value: jsonValue('payload', 'key') }).maybeSingle()
const only: { value: Json } | null = jsonOnly.data
const throwing = await selectJson(db.from('notes').select().throwOnError(), 'id', { value: jsonValue('payload', 'key') }).single()
const throwingID: number = throwing.data.id
// @ts-expect-error Wildcard base selections already select payload.
selectJson(db.from('notes'), '*', { payload: jsonText('payload', 'key') })
// @ts-expect-error JSON text filter values are strings.
query.eq('payload->>status', 1)
// @ts-expect-error Literal select strings preserve projection inference.
selectJson(db.from('notes'), '' as string, { status: jsonText('payload', 'key') })
const dynamicAliases: Record<string, JsonProjection<'payload', true>> = { id: jsonText('payload', 'key') }
// @ts-expect-error Unknown aliases could silently replace selected fields.
selectJson(db.from('notes'), 'id', dynamicAliases)
const numericAliases: Record<number, JsonProjection<'payload', true>> = { 0: jsonText('payload', 'key') }
// @ts-expect-error Dynamic numeric aliases can also replace selected fields.
selectJson(db.from('notes'), '0:id', numericAliases)
// @ts-expect-error Numeric alias keys also cannot replace selected aliases.
selectJson(db.from('notes'), '0:id', { 0: jsonText('payload', 'key') })
void [rows, wrongID, wrongText, only, throwingID]
