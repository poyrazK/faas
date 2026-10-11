import test from 'node:test'
import assert from 'node:assert/strict'
import { viewOrigins, viewRelationships, normalize, fingerprint } from '../types.mjs'
import { limits } from '../config.mjs'

const entry = (number, id, column, name = 'field', junk = false) => `{TARGETENTRY :expr {VAR :varno 1} :resno ${number} :resname ${name} :resorigtbl ${id} :resorigcol ${column} :resjunk ${junk}}`
const action = (...entries) => `({QUERY :commandType 1 :rtable ({RANGETBLENTRY :subquery {QUERY :targetList (${entry(1, 999, 1)})}}) :targetList (${entries.join(' ')}) :setOperations <>})`
const column = (id, relation, number, name, kind = 'r', schema = 'api') => ({ id, relation, number, name, kind, schema, type: '1', required: false, identity: '', generated: '', defaulted: false, insertable: false, updatable: false })
const fk = { schema: 'api', relation: 'orders', name: 'orders_customer_fkey', referenced_schema: 'api', referenced_relation: 'customers', columns: ['subject', 'customer_id'], referenced_columns: ['subject', 'id'], one_to_one: false }
const columns = [column('1', 'orders', 1, 'subject'), column('1', 'orders', 2, 'customer_id'), column('2', 'customers', 1, 'subject'), column('2', 'customers', 2, 'id'),
  column('3', 'order_view', 1, 'owner', 'v'), column('3', 'order_view', 2, 'buyer', 'v'), column('4', 'customer_view', 1, 'owner', 'v'), column('4', 'customer_view', 2, 'customer_key', 'v')]
const views = [{ id: '3', schema: 'api', relation: 'order_view', action: action(entry(1, 1, 1), entry(2, 1, 2)) },
  { id: '4', schema: 'api', relation: 'customer_view', action: action(entry(1, 2, 1), entry(2, 2, 2)) }]

test('origin reader isolates root projections and handles PostgreSQL escaped identifiers', () => {
  assert.deepEqual(viewOrigins(action(entry(1, 12, 3, 'name\\ :targetList\\ \\(\\{\\ \\}'), entry(2, 12, 4, ':resorigtbl'), entry(3, 0, 0), entry(4, 12, 5, 'junk', true))), [
    { number: 1, id: '12', column: 3 }, { number: 2, id: '12', column: 4 },
  ])
  for (const value of [null, '', '({QUERY})', action(entry(1, 12, 3)).slice(0, -1), action(entry(1, 12, 3)) + '{}',
    action(entry(1, 12, 3)).replace(':setOperations <>', ':setOperations {SETOPERATIONSTMT}'),
    action(entry(1, 12, 3)).replace(':commandType 1', ':commandType 2'),
    action(entry(1, 12, 3)).replace(':resno 1 :resname field :resorigtbl 12', ':resno 1 :resno 2 :resname field :resorigtbl 12'), 'x'.repeat(limits.outputBytes + 1)]) assert.deepEqual(viewOrigins(value), [])
})

test('complete ordered keys produce table/view, view/table and view/view relationships', () => {
  const inferred = viewRelationships(columns, [fk], views)
  assert.equal(inferred.length, 3)
  assert.deepEqual(inferred.find(r => r.relation === 'order_view' && r.referenced_relation === 'customer_view').columns, ['owner', 'buyer'])
  assert.deepEqual(inferred.find(r => r.relation === 'order_view' && r.referenced_relation === 'customer_view').referenced_columns, ['owner', 'customer_key'])
  const types = [{ id: '1', schema: 'pg_catalog', name: 'text', element: '0' }]
  const snapshot = normalize(columns, types, [fk, ...inferred], ['api'])
  assert.equal(snapshot.tables.find(t => t.name === 'order_view').relationships.length, 2)
  const changed = viewRelationships(columns, [{ ...fk, one_to_one: true }], views)
  assert.ok(changed.every(r => r.one_to_one))
  assert.notEqual(fingerprint(snapshot), fingerprint(normalize(columns, types, [fk, ...changed], ['api'])))
})

test('view chains resolve visible columns; partial keys, expressions, cycles and hidden origins stay absent', () => {
  const chainColumns = [...columns, column('5', 'chain', 1, 'tenant', 'v'), column('5', 'chain', 2, 'customer', 'v')]
  const chain = { id: '5', schema: 'api', relation: 'chain', action: action(entry(1, 3, 1), entry(2, 3, 2)) }
  assert.ok(viewRelationships(chainColumns, [fk], [...views, chain]).some(r => r.relation === 'chain'))
  for (const modified of [action(entry(1, 1, 1)), action(entry(1, 1, 1), entry(2, 0, 0)), action(entry(1, 99, 1), entry(2, 99, 2)), action(entry(1, 3, 1), entry(2, 3, 2))]) {
    assert.ok(!viewRelationships(columns, [fk], [{ ...views[0], action: modified }]).length)
  }
  assert.ok(!viewRelationships(columns.filter(c => c.id !== '1'), [fk], views).some(r => r.relation === 'order_view'))
  assert.deepEqual(viewRelationships(columns, [{ ...fk, referenced_schema: 'other' }], views), [])
})

test('repeated column aliases remain distinct join paths and expansion is bounded', () => {
  const duplicateColumns = [...columns, column('3', 'order_view', 3, 'buyer_again', 'v')]
  const duplicate = { ...views[0], action: action(entry(1, 1, 1), entry(2, 1, 2), entry(3, 1, 2)) }
  const inferred = viewRelationships(duplicateColumns, [fk], [duplicate])
  assert.deepEqual(inferred.map(r => r.columns), [['owner', 'buyer'], ['owner', 'buyer_again']])
  const wideColumns = Array.from({ length: limits.relations + 1 }, (_, i) => column('3', 'order_view', i + 1, `key_${i}`, 'v'))
  const wideView = { ...views[0], action: action(...wideColumns.map(c => entry(c.number, 1, 2))) }
  assert.throws(() => viewRelationships([...columns, ...wideColumns], [{ ...fk, columns: ['customer_id'], referenced_columns: ['id'] }], [wideView]), /Too many inferred view relationships/)
})
