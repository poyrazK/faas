import { createDataClient } from '../src/index.js'
import type { Database } from './database.types.js'

// Aggregate selects (DATA_API_AGGREGATES) keep generated result types.
const client = createDataClient<Database>({ url: 'https://data.example', accessToken: async () => 'jwt' }).schema('api')
const totals = await client.from('notes').select('total:id.sum(), notes:count()').single()
if (totals.data) {
  const total: number = totals.data.total
  const notes: number = totals.data.notes
  // @ts-expect-error Aggregates are numbers, not strings.
  const wrong: string = totals.data.total
  void [total, notes, wrong]
}
const grouped = await client.from('notes').select('state, count()')
for (const group of grouped.data ?? []) {
  const state: 'open' | 'closed' | null = group.state
  const count: number = group.count
  void [state, count]
}
