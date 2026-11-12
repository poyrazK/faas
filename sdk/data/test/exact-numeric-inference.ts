import { createDataClient, ExactNumber, type QueryData } from '../src/index.js'
import { liveRead } from '../src/live/index.js'

type Database = { api: {
  Tables: { measurements: {
    Row: { id: ExactNumber; label: string }
    Insert: { id: ExactNumber; label: string }
    Update: { id?: ExactNumber; label?: string }
    Relationships: []
  } }
  Views: Record<never, never>
  Functions: { echo_number: { Args: { value: ExactNumber }; Returns: ExactNumber } }
} }
const db = createDataClient<Database>({ url: 'https://data.example', accessToken: 'token', numericMode: 'exact' }).schema('api')
const query = db.from('measurements').select('id,label')
type Rows = QueryData<typeof query>
const row: Rows[number] = { id: new ExactNumber('9007199254740993'), label: '9007199254740993' }
const exact: ExactNumber = row.id
void exact
void db.from('measurements').insert(row)
void db.rpc('echo_number', { value: row.id })
const live = liveRead(query)
const parsed = live.parse(['9007199254740993','label'], [{name:'id',type:'int8'}, {name:'label',type:'text'}])
const liveId: ExactNumber = parsed.id
void liveId
// @ts-expect-error a numeric-looking string is not an exact numeric value
void db.from('measurements').insert({ id: '9007199254740993', label: 'text' })
// @ts-expect-error coercion requires an explicit application rounding policy
const number: number = row.id
void number
