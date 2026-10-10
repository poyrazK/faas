import test from 'node:test'
import assert from 'node:assert/strict'
import { normalize, generate, enrichOpenAPI } from '../types.mjs'

const column = (relation, kind = 'r') => ({ schema: 'api', relation, kind, name: 'id', type: '1', required: true, identity: '', generated: '', defaulted: false, insertable: false, updatable: false })
const columns = [column('orders'), column('invoices'), column('invoice_cards', 'v')]
const types = [{ id: '1', name: 'int4', schema: 'pg_catalog', element: '0' },
  ...['orders', 'invoices', 'invoice_cards'].map((name, i) => ({ id: String(i + 2), name, schema: 'api', kind: 'c', element: '0', selectable: true, accessible: true }))]
const fn = { schema: 'api', name: 'latest_invoice', overloads: 1, names: ['source_row'], arguments: ['2'], defaults: 0, return_type: '3', setof: true, modes: null, volatility: 's', rows: 1 }
const inspect = (functions, catalog = types) => normalize(columns, catalog, [], ['api'], functions)

test('computed joins preserve source/target/cardinality while direct RPCs still return arrays', () => {
  const snapshot = inspect([fn, { ...fn, name: 'invoice_history', rows: 1000 }, { ...fn, name: 'invoice_summary', return_type: '4' }, { ...fn, name: 'search_invoices', arguments: ['1'], rows: 1 }])
  assert.equal(snapshot.version, 3)
  const latest = snapshot.functions.find(f => f.name === fn.name)
  assert.deepEqual(latest.setof, { from: 'orders', to: 'invoices', one_to_one: true })
  assert.equal(latest.returns, `Database["api"]["Tables"]["invoices"]['Row'][]`)
  assert.equal(latest.args[0].type, `Database["api"]["Tables"]["orders"]['Row']`)
  assert.deepEqual(snapshot.functions.find(f => f.name === 'search_invoices').setof, { from: '', to: 'invoices', one_to_one: false })
  assert.equal(snapshot.functions.find(f => f.name === 'invoice_history').setof.one_to_one, false)
  assert.equal(snapshot.functions.find(f => f.name === 'invoice_summary').setof.to, 'invoice_cards')
  const output = generate(snapshot)
  assert.ok(output.includes(`Returns: Database["api"]["Tables"]["invoices"]['Row']\n        SetofOptions: {"from":"orders","to":"invoices","isOneToOne":true,"isSetofReturn":true,"isNotNullable":false}`))
  const spec = enrichOpenAPI({ paths: { '/orders': { get: { responses: { 200: {} } } }, '/rpc/latest_invoice': { post: { responses: { 200: {} } } } } }, snapshot, 'api')
  assert.deepEqual(spec.paths['/rpc/latest_invoice'].post.responses[200].schema, { type: 'array', items: { $ref: '#/definitions/api.invoices.Row' } })
  assert.equal(spec.paths['/orders'].get['x-gregale-computed-relationships'].length, 3)
})

test('computed metadata excludes unsupported signatures, shadowing and inaccessible rows', () => {
  for (const change of [{ volatility: 'v' }, { overloads: 2 }, { names: [] }, { defaults: 1 }, { setof: false }, { name: 'invoices' }, { name: 'id' }, { modes: ['o'] }, { arguments: ['2', '1'], names: ['source_row', 'other'] }]) {
    assert.deepEqual(inspect([{ ...fn, ...change }]).functions, [], JSON.stringify(change))
  }
  for (const change of [{ accessible: false }, { selectable: false }, { schema: 'private' }]) {
    assert.deepEqual(inspect([fn], types.map(t => t.id === '2' ? { ...t, ...change } : t)).functions, [])
  }
  assert.deepEqual(inspect([fn], types.map(t => t.id === '3' ? { ...t, schema: 'other' } : t)).functions, [])
  assert.deepEqual(inspect([fn], types.map(t => t.id === '1' ? { ...t, name: 'point' } : t)).functions, [], 'unknown source-row fields cannot become RPC arguments')
  assert.deepEqual(normalize(columns, types, [{ schema: 'api', relation: 'orders', name: fn.name, columns: ['id'], referenced_schema: 'api', referenced_relation: 'invoices', referenced_columns: ['id'], one_to_one: false }], ['api'], [fn]).functions, [], 'a computed name cannot shadow a foreign-key path')
})
