import test from 'node:test'
import assert from 'node:assert/strict'
import { normalize, generate, fingerprint, enrichOpenAPI } from '../types.mjs'

const types = [
  { id: '1', schema: 'pg_catalog', name: 'text', element: '0' },
  { id: '2', schema: 'api', name: 'location', kind: 'c', element: '0' },
  { id: '3', schema: 'api', name: '_location', element: '2' },
  { id: '4', schema: 'api', name: 'profile', kind: 'c', element: '0' },
  { id: '5', schema: 'api', name: 'required_text', kind: 'd', base: '1', element: '0', required: true },
]
const fields = [
  { id: '2', schema: 'api', name: 'location', field: 'label', type: '5', required: false },
  { id: '4', schema: 'api', name: 'profile', field: 'home', type: '2', required: false },
  { id: '4', schema: 'api', name: 'profile', field: 'history', type: '3', required: false },
]
const fn = { schema: 'api', name: 'profile_value', overloads: 1, names: ['value'], arguments: ['4'], defaults: 0, return_type: '4', setof: false, modes: null }
const ref = name => `Database["api"]["CompositeTypes"]["${name}"]`

test('named composite fields, nullable nested arrays, RPC shapes and OpenAPI share one contract', () => {
  const snapshot = normalize([], types, [], ['api'], [fn], fields)
  assert.equal(snapshot.version, 2)
  assert.deepEqual(snapshot.composites, [
    { schema: 'api', name: 'location', fields: [{ name: 'label', type: 'string', nullable: true }] },
    { schema: 'api', name: 'profile', fields: [{ name: 'home', type: ref('location'), nullable: true }, { name: 'history', type: `(${ref('location')} | null)[]`, nullable: true }] },
  ])
  assert.equal(snapshot.functions[0].returns, ref('profile'))
  assert.equal(normalize([], types, [], ['api'], [{ ...fn, setof: true }], fields).functions[0].returns, `${ref('profile')}[]`)
  const output = generate(snapshot)
  assert.match(output, /export type CompositeTypes</)
  assert.ok(output.includes(`"home": ${ref('location')} | null`))
  const spec = enrichOpenAPI({ paths: { '/rpc/profile_value': { post: { responses: { 200: {} } } } } }, snapshot, 'api')
  assert.deepEqual(spec.definitions['api.profile.Composite'].properties.history, { type: 'array', items: { $ref: '#/definitions/api.location.Composite', 'x-nullable': true }, 'x-nullable': true })
  assert.deepEqual(spec.paths['/rpc/profile_value'].post.responses[200].schema, { $ref: '#/definitions/api.profile.Composite' })
  assert.notEqual(fingerprint(snapshot), fingerprint(normalize([], types, [], ['api'], [fn], fields.slice(1))))
})

test('hidden attributes, table row types, unsupported dependencies and cycles cannot enable RPCs', () => {
  for (const extra of [
    { id: '6', schema: 'private', name: 'secret', kind: 'c', element: '0' },
    { id: '6', schema: 'api', name: 'secret', kind: 'c', element: '0', accessible: false },
    { id: '6', schema: 'api', name: 'private_table', kind: 'c', element: '0' },
    { id: '6', schema: 'pg_catalog', name: 'record', element: '0' },
    { id: '6', schema: 'api', name: 'secret_enum', kind: 'e', element: '0', labels: ['secret-label'], accessible: false },
  ]) {
    const snapshot = normalize([], [...types, extra], [], ['api'], [fn], [...fields, { id: '4', schema: 'api', name: 'profile', field: 'opaque', type: '6', required: false }])
    assert.deepEqual(snapshot.functions, [])
    assert.equal(snapshot.composites[1].fields[2].type, 'unknown')
    assert.doesNotMatch(generate(snapshot), /secret-label|private_table|"secret"/)
  }
  const cycle = [...fields, { id: '2', schema: 'api', name: 'location', field: 'cycle', type: '4', required: false }]
  assert.deepEqual(normalize([], types, [], ['api'], [fn], cycle).functions, [])
  const empty = normalize([], types, [], ['api'], [], [{ id: '2', schema: 'api', name: 'location', field: null }])
  assert.deepEqual(empty.composites[0].fields, [])
})
