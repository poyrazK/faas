import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { pathToFileURL, fileURLToPath } from 'node:url'
import { execFileSync } from 'node:child_process'

test('generated helpers enforce rows, write permissions, enums and explicit schemas', { skip: !process.env.DATA_API_RUNTIME_DIR }, async t => {
  const runtime = process.env.DATA_API_RUNTIME_DIR
  const { generate } = await import(pathToFileURL(join(runtime, 'types.mjs')))
  const directory = await mkdtemp(join(tmpdir(), 'data-api-type-helpers-'))
  t.after(() => rm(directory, { recursive: true, force: true }))
  const column = { name: 'id', type: 'number', nullable: false, optional: true, insertable: false, updatable: false }
  const snapshot = { version: 2, schemas: ['api', 'other'], functions: [], enums: { 'api.state': ['open', 'closed'] }, composites: [
    { schema: 'api', name: 'address', fields: [{ name: 'city', type: 'string', nullable: true }] },
    { schema: 'other', name: 'address', fields: [{ name: 'postcode', type: 'number', nullable: true }] },
  ], tables: [
    { schema: 'api', name: 'orders', view: false, relationships: [], columns: [column, { name: 'state', type: '"open" | "closed"', nullable: false, optional: false, insertable: true, updatable: true }] },
    { schema: 'api', name: 'order_ids', view: true, relationships: [], columns: [column] },
    { schema: 'other', name: 'items', view: false, relationships: [], columns: [column] },
  ] }
  await writeFile(join(directory, 'database.types.ts'), generate(snapshot))
  await writeFile(join(directory, 'inference.ts'), `import type {Tables,TablesInsert,TablesUpdate,Enums,CompositeTypes} from './database.types.js';
const address: CompositeTypes<'address'> = {city:null};
const otherAddress: CompositeTypes<'address','other'> = {postcode:123};
// @ts-expect-error Composite helpers preserve schema namespaces.
const wrongAddress: CompositeTypes<'address','other'> = {city:'Istanbul'};
// @ts-expect-error Composite keys remain required, even when nullable.
const missingField: CompositeTypes<'address'> = {};
// @ts-expect-error Unknown composite names are rejected.
type MissingComposite = CompositeTypes<'missing'>;
const row: Tables<'orders'> = {id:1,state:'open'};
const view: Tables<'order_ids'> = {id:1};
const other: Tables<'items','other'> = {id:2};
const insert: TablesInsert<'orders'> = {state:'open'};
const update: TablesUpdate<'orders'> = {state:'closed'};
const state: Enums<'state'> = 'closed';
// @ts-expect-error Generated IDs cannot be inserted.
const wrongID: TablesInsert<'orders'> = {id:1,state:'open'};
// @ts-expect-error Insert contracts keep required fields.
const missing: TablesInsert<'orders'> = {};
// @ts-expect-error Unknown relations are rejected.
type Unknown = Tables<'missing'>;
// @ts-expect-error Views are read contracts.
type WritableView = TablesUpdate<'order_ids'>;
// @ts-expect-error Enum values come from the schema.
const wrongState: Enums<'state'> = 'invalid';
void [row,view,other,insert,update,state,wrongID,missing,wrongState,address,otherAddress,wrongAddress,missingField];`)
  const tsc = fileURLToPath(new URL('../../sdk/data/node_modules/typescript/bin/tsc', import.meta.url))
  assert.doesNotThrow(() => execFileSync(process.execPath, [tsc, '--strict', '--noEmit', '--skipLibCheck', join(directory, 'inference.ts')], { encoding: 'utf8' }))
})
