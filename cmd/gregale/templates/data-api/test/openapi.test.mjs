import test from 'node:test'
import assert from 'node:assert/strict'
import { contractSchema, enrichOpenAPI, generate } from '../types.mjs'

test('OpenAPI type mapping preserves nested arrays, enum punctuation and composite RPC results', () => {
  assert.deepEqual(contractSchema('(string | null)[]'), { type: 'array', items: { type: 'string', 'x-nullable': true } })
  assert.deepEqual(contractSchema('"a | b" | "quote\\\""'), { type: 'string', enum: ['a | b', 'quote"'] })
  assert.deepEqual(contractSchema('Database["api"]["Tables"]["notes"][\'Row\'][]'), { type: 'array', items: { $ref: '#/definitions/api.notes.Row' } })
  assert.deepEqual(contractSchema('Json'), {})
  assert.deepEqual(contractSchema('unknown'), {})
})

test('OpenAPI and TypeScript share row, write and approved RPC contracts', () => {
  const snapshot = { version: 1, schemas: ['api'], enums: {}, tables: [{ schema: 'api', name: 'notes', view: false, relationships: [], columns: [
    { name: 'id', type: 'number', nullable: false, optional: true, insertable: false, updatable: false },
    { name: 'body', type: 'string', nullable: false, optional: false, insertable: true, updatable: true },
    { name: 'tags', type: '(string | null)[]', nullable: true, optional: true, insertable: true, updatable: true },
  ] }], functions: [{ schema: 'api', name: 'create_note', args: [{ name: 'body', type: 'string', optional: false }, { name: 'priority', type: 'number', optional: true }], returns: 'Database["api"]["Tables"]["notes"][\'Row\'][]' }] }
  const op = () => ({ parameters: [{ in: 'body' }], responses: { 200: { description: 'OK' }, 204: { description: 'No body' } } })
  const document = enrichOpenAPI({ swagger: '2.0', paths: { '/notes': { get: op(), post: op(), patch: op() }, '/rpc/create_note': { post: op() } } }, snapshot, 'api')
  const definitions = document.definitions
  assert.deepEqual(definitions['api.notes.Row'].required, ['id', 'body', 'tags'])
  assert.deepEqual(definitions['api.notes.Insert'].required, ['body'])
  assert.equal(definitions['api.notes.Insert'].properties.id, undefined)
  assert.equal(definitions['api.notes.Update'].required, undefined)
  assert.deepEqual(definitions['api.create_note.Args'].required, ['body'])
  assert.equal(definitions['api.create_note.Args'].properties.body['x-nullable'], true)
  assert.deepEqual(document.paths['/rpc/create_note'].post.responses[200].schema, contractSchema(snapshot.functions[0].returns))
  assert.equal(document.paths['/notes'].post.responses[204].schema, undefined)
  assert.deepEqual(document.security, [{ bearer: [] }])
  assert.match(generate(snapshot), new RegExp(document['x-gregale-schema-fingerprint']))
  assert.match(generate(snapshot), /"id"\?: never/)
  assert.match(generate(snapshot), /"body": string \| null/)
  assert.equal(document.paths['/notes'].get.responses[401].schema.$ref, '#/definitions/GregaleProblem')
})

test('zero-argument void RPCs omit required and response bodies; profiles stay isolated', () => {
  const snapshot = { schemas: ['api', 'other'], tables: [], enums: {}, functions: [
    { schema: 'api', name: 'ping', args: [], returns: 'undefined' },
    { schema: 'other', name: 'hidden', args: [], returns: 'string | null' },
  ] }
  const document = enrichOpenAPI({ paths: { '/rpc/ping': { post: { responses: { 200: { schema: { type: 'string' } } } } } } }, snapshot, 'api')
  assert.equal(document.definitions['api.ping.Args'].required, undefined)
  assert.equal(document.definitions['other.hidden.Args'], undefined)
  assert.equal(document.paths['/rpc/ping'].post.parameters[0].required, false)
  assert.equal(document.paths['/rpc/ping'].post.responses[200].schema, undefined)
})
