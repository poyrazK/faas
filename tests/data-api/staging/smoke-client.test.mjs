import test from 'node:test'
import assert from 'node:assert/strict'
import { execFile } from 'node:child_process'
import { cp, mkdir, mkdtemp, rm, symlink, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { promisify } from 'node:util'
import { generate } from '../../../cmd/gregale/templates/data-api/type-render.mjs'

test('the compiled smoke client writes a JSON bigint and rejects a rounded read', async t => {
  const directory = await mkdtemp(join(tmpdir(), 'data-api-smoke-client-'))
  t.after(() => rm(directory, { recursive: true, force: true }))
  const sdk = fileURLToPath(new URL('../../../sdk/data/', import.meta.url))
  await mkdir(join(directory, 'node_modules/@gregale'), { recursive: true })
  await symlink(sdk, join(directory, 'node_modules/@gregale/data'), 'dir')
  for (const file of ['client.ts', 'smoke-client.ts']) {
    await cp(new URL(file, import.meta.url), join(directory, file))
  }
  await writeFile(join(directory, 'package.json'), '{"type":"module"}')
  await writeFile(join(directory, 'database.types.ts'), generate({
    schemas: ['api'], enums: {},
    tables: [{ name: 'notes', schema: 'api', view: false, relationships: [], columns: [
      { name: 'id', type: 'number' },
      { name: 'subject', type: 'string', insertable: true },
      { name: 'body', type: 'string', insertable: true, updatable: true },
      { name: 'exact_value', type: 'number', nullable: true, optional: true, insertable: true, updatable: true },
    ] }],
  }, undefined, { numericMode: 'exact' }))
  await promisify(execFile)(join(sdk, 'node_modules/.bin/tsc'), [
    '--strict', '--target', 'ES2022', '--module', 'NodeNext', '--outDir', 'dist', 'smoke-client.ts',
  ], { cwd: directory })
  const client = await import(pathToFileURL(join(directory, 'dist/smoke-client.js')))
  const base = 'https://data.example'
  const calls = []
  const fetchCRUD = async (_input, init) => {
    const headers = new Headers(init.headers)
    calls.push([init.method, headers.get('authorization')])
    switch (calls.length) {
      case 1: return Response.json({ id: 7, body: 'canary' })
      case 2: return Response.json([])
      case 3: return Response.json({ code: '42501', message: 'denied' }, { status: 403 })
      case 4: return Response.json({ id: 7, body: 'updated' })
      default: assert.fail('unexpected CRUD request')
    }
  }
  const id = await client.exercise(base, 'alice', 'bob', fetchCRUD)
  assert.equal(id.source, '7')
  assert.deepEqual(calls, [['POST', 'Bearer alice'], ['GET', 'Bearer bob'], ['POST', 'Bearer bob'], ['PATCH', 'Bearer alice']])

  for (const value of ['9007199254740993', '9007199254740992']) {
    const requests = []
    const fetchExact = async (input, init) => {
      requests.push([init.method, new URL(input).searchParams.get('id'), init.body])
      if (init.method === 'PATCH') return Response.json({ id: 7 })
      return new Response(`{"exact_value":${value}}`, { headers: { 'Content-Type': 'application/json' } })
    }
    const roundTrip = client.exerciseExact(base, 'alice', id, fetchExact)
    if (value === '9007199254740993') await roundTrip
    else await assert.rejects(roundTrip, /exact_bigint_read_failed/)
    assert.deepEqual(requests, [['PATCH', 'eq.7', '{"exact_value":9007199254740993}'], ['GET', 'eq.7', undefined]])
  }
})
