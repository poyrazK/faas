import { createDataClient, cursorPage, cursorFrom, type QueryData } from '../src/index.js'
import type { Database } from './database.types.js'
const db = createDataClient<Database>({ url: 'https://data.example', accessToken: 'jwt' }).schema('api')
const order = [{ column: 'body' }, { column: 'id', unique: true }] as const
const after = cursorFrom({ body: 'hello', id: 1 }, order)
const page = cursorPage(db.from('notes').select('id,body,comments(id)'), { order, size: 20, after })
const data: QueryData<typeof page> = [{ id: 1, body: 'hi', comments: [{ id: 2 }] }]
// @ts-expect-error Preserve nested projection.
const bad: QueryData<typeof page> = [{ id: 1, body: 'hi', comments: [{ body: 'no' }] }]
// @ts-expect-error Check cursor types against source columns.
cursorPage(db.from('notes').select(), { order, size: 2, after: ['hi', 'wrong'] })
// @ts-expect-error Unknown ordering columns.
cursorPage(db.from('notes').select(), { order: [{ column: 'absent', unique: true }], size: 2 })
// @ts-expect-error Cursor extraction requires selected ordering columns.
cursorFrom({ id: 1 }, order)
void [data, bad]

// @ts-expect-error Pagination requires a read returning rows.
cursorPage(db.from('notes').select().single(), { order, size: 2 })
// @ts-expect-error Pagination must not mutate data.
cursorPage(db.from('notes').delete().select(), { order, size: 2 })

import { createCursorPager } from '../src/index.js'
const pager = await createCursorPager({ key: new Uint8Array(32) })
const scope = { identity: 'trusted-alice', schemaFingerprint: 'a'.repeat(64) }
const opaque = await pager.page(db.from('notes').select('id,body,comments(id)'), { order, size: 20, scope })
const opaqueData: typeof opaque.result.data = [{ id: 1, body: 'hi', comments: [{ id: 2 }] }]
// @ts-expect-error Cursor ordering columns must be included in the projection.
await pager.page(db.from('notes').select('id'), { order, size: 20, scope })
// @ts-expect-error Cursor ordering must refer to schema columns.
await pager.page(db.from('notes').select(), { order: [{ column: 'missing', unique: true }], size: 20, scope })
// @ts-expect-error Opaque cursors are strings, not raw value arrays.
await pager.page(db.from('notes').select('id,body'), { order, size: 20, scope, after })
// @ts-expect-error Opaque pagination must not mutate data.
await pager.page(db.from('notes').delete().select('id,body'), { order, size: 20, scope })
void opaqueData
