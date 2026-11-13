import test from 'node:test'
import assert from 'node:assert/strict'
import { contractSchema, enrichOpenAPI, generate } from '../types.mjs'
import { openAPI31 } from '../openapi31.mjs'

test('OpenAPI 3.1 converts unions, nullable references, bodies, headers and shared parameters', () => {
  const document = openAPI31({ swagger: '2.0', info: { title: 'Data', version: '1' }, basePath: '/rest/v1', consumes: ['application/json'], parameters: {
    payload: { in: 'body', name: 'body', required: true, schema: { $ref: '#/definitions/Row', 'x-gregale-bulk-schema': { type: 'array', items: { $ref: '#/definitions/Row' } } } },
    limit: { in: 'query', name: 'limit', type: 'integer', minimum: 0 },
  }, definitions: { Row: { type: 'object', properties: { optional: { type: 'string', enum: ['yes'], 'x-nullable': true }, nested: { $ref: '#/definitions/Row', 'x-nullable': true } } } }, paths: {
    '/notes': { post: { parameters: [{ $ref: '#/parameters/payload' }, { $ref: '#/parameters/limit' }], responses: { 200: { description: 'OK', schema: { $ref: '#/definitions/Row' }, headers: { 'X-Request-Id': { type: 'string', description: 'ID' } } } } } },
  } })
  assert.equal(document.openapi, '3.1.0')
  assert.deepEqual(document.servers, [{ url: '/rest/v1' }])
  assert.deepEqual(document.components.schemas.Row.properties.optional, { type: ['string', 'null'], enum: ['yes', null] })
  assert.deepEqual(document.components.schemas.Row.properties.nested, { anyOf: [{ $ref: '#/components/schemas/Row' }, { type: 'null' }] })
  const post = document.paths['/notes'].post
  assert.equal(post.requestBody.required, true)
  assert.equal(post.requestBody.content['application/json'].schema.anyOf[1].items.$ref, '#/components/schemas/Row')
  assert.equal(post.responses[200].content['application/json'].schema.$ref, '#/components/schemas/Row')
  assert.deepEqual(post.responses[200].headers['X-Request-Id'], { description: 'ID', schema: { type: 'string' } })
  assert.equal(post.parameters.length, 1)
  assert.equal(document.components.parameters.payload, undefined)
  assert.doesNotMatch(JSON.stringify(document), /#\/(definitions|parameters)\//)
})

test('lossless TypeScript keeps the serving fingerprint and quoted number labels', () => {
  const snapshot = { schemas: ['api'], tables: [{ schema: 'api', name: 'items', view: false, relationships: [], columns: [{ name: 'id', type: 'number', nullable: false }, { name: 'kind', type: '"number"', nullable: false }] }], enums: {}, functions: [] }
  const regular = generate(snapshot)
  const lossless = generate(snapshot, undefined, { numericMode: 'lossless' })
  assert.equal(/Schema fingerprint: (.+)/.exec(regular)[1], /Schema fingerprint: (.+)/.exec(lossless)[1])
  assert.match(lossless, /"id": string/)
  assert.match(lossless, /"kind": "number"/)
  assert.doesNotMatch(lossless, /Json = string \| number/)
})

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
  assert.equal(document.paths['/notes'].get.responses[429].schema.$ref, '#/definitions/GregaleProblem')
  assert.equal(document.paths['/notes'].get.responses[429].headers['Retry-After'].type, 'integer')
  assert.equal(document.definitions.GregaleProblem.properties.retry_after_seconds.minimum, 1)
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

test('DAPI012 cross-profile RPC row references resolve without exposing cross-profile paths', () => {
  const snapshot = { schemas: ['api','other'], tables: [{ schema:'other',name:'items',view:false,relationships:[],columns:[{name:'id',type:'number',nullable:false}] }], enums:{}, functions:[{schema:'api',name:'items',args:[],returns:'Database["other"]["Tables"]["items"][\'Row\'][]'}] }
  const swagger = enrichOpenAPI({paths:{'/rpc/items':{post:{responses:{200:{description:'OK'}}}}}},snapshot,'api')
  assert.ok(swagger.definitions['other.items.Row'])
  assert.equal(swagger.paths['/items'],undefined)
  const doc=openAPI31(swagger)
  assert.equal(doc.paths['/rpc/items'].post.responses[200].content['application/json'].schema.items.$ref,'#/components/schemas/other.items.Row')
  assert.ok(doc.components.schemas['other.items.Row'])
})

test('DAPI013 schema dictionary keys and literal examples remain data', () => {
 const properties={'$ref':{type:'string'},'x-nullable':{type:'number'},'example':{type:'string'}}
 const literal={'$ref':'#/definitions/literal','x-nullable':true,properties:{'$ref':'unchanged'}}
 const result=openAPI31({definitions:{Row:{type:'object',properties,example:literal,default:literal,enum:[literal]}}})
 assert.deepEqual(result.components.schemas.Row.properties,properties)
 assert.deepEqual(result.components.schemas.Row.example,literal)
 assert.deepEqual(result.components.schemas.Row.default,literal)
 assert.deepEqual(result.components.schemas.Row.enum,[literal])
})

test('DAPI006 exact numeric types keep the contract fingerprint and string literals',()=>{
 const snapshot={schemas:['api'],tables:[{schema:'api',name:'items',view:false,relationships:[],columns:[{name:'number',type:'number',nullable:false},{name:'kind',type:'"number"',nullable:false}]}],functions:[],enums:{}}
 const standard=generate(snapshot),exact=generate(snapshot,undefined,{numericMode:'exact'})
 assert.match(exact,/import type \{ ExactNumber \} from '@gregale\/data'/)
 assert.match(exact,/"number": ExactNumber/)
 assert.match(exact,/"kind": "number"/)
 assert.equal(/Schema fingerprint: (.+)/.exec(exact)[1],/Schema fingerprint: (.+)/.exec(standard)[1])
})
