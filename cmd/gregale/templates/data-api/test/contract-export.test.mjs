import assert from 'node:assert/strict'
import test from 'node:test'
import { normalize } from '../types.mjs'
import { contractArtifactBytes, fingerprint, generate, generateValidators } from '../type-render.mjs'

const column = (relation, name, type) => ({
  schema: 'api', relation, name, type, kind: 'r',
  identity: '', insertable: true, updatable: true,
})

const baseType = (id, name) => ({ id, name, schema: 'pg_catalog', kind: 'b', element: '0' })

test('numeric warnings include exposed bigint domains and numeric arrays without changing the fingerprint', () => {
  const types = [
    baseType('1', 'int8'), baseType('2', 'numeric'), baseType('3', 'int4'),
    { id: '4', schema: 'api', name: 'order_id', kind: 'd', base: '1', element: '0' },
    { id: '5', schema: 'pg_catalog', name: '_numeric', kind: 'b', element: '2' },
  ]
  const columns = [column('orders', 'id', '4'), column('orders', 'amounts', '5'), column('orders', 'version', '3')]
  const risks = new Set()
  const snapshot = normalize(columns, types, [], ['api'], [], [], risks)
  assert.deepEqual([...risks].sort(), ['bigint', 'numeric'])
  assert.equal(fingerprint(snapshot), fingerprint(normalize(columns, types, [], ['api'])))
  const ordinary = generate(snapshot)
  assert.doesNotMatch(ordinary, /dataAPINumericMode/)
  assert.match(ordinary, /"id": number/)
  const exact = generate(snapshot, fingerprint(snapshot), { numericMode: 'exact' })
  assert.match(exact, /"id": ExactNumber/)
  assert.match(exact, /export const dataAPINumericMode = "exact" as const/)
  risks.clear()
  normalize([column('orders', 'version', '3')], types, [], ['api'], [], [], risks)
  assert.equal(risks.size, 0)
})

test('a large validator export fits the artifact budget and reports the legacy size limit', () => {
  const columns = Array.from({ length: 80 * 40 }, (_, index) =>
    column(`account_records_${Math.floor(index / 40)}`, `customer_attribute_${index % 40}`, '25'))
  const snapshot = normalize(columns, [baseType('25', 'text')], [], ['api'])
  assert.ok(Buffer.byteLength(generate(snapshot)) < 1024 * 1024)
  assert.throws(() => generateValidators(snapshot), /validators exceeds 1048576 bytes/)
  const validators = generateValidators(snapshot, [], fingerprint(snapshot), { outputBytes: contractArtifactBytes })
  assert.match(validators, /export const inputValidators/)
  assert.ok(Buffer.byteLength(validators) > 1024 * 1024)
})
