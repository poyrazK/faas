import test from 'node:test'
import assert from 'node:assert/strict'
import { validateWriteQualification } from '../../cmd/gregale/templates/data-api/dev-access.mjs'

const fingerprint = 'a'.repeat(64)
const project = { identities: { alice: { subject: 'alice' }, bob: { subject: 'bob' } } }
const scope = { schema: 'api', table: 'notes', operations: ['insert', 'update', 'delete'] }
const check = (name, identity, method, fixture, body, status, rows, outcome) => ({ name, identity, method, fixture, body, expect: { status, rows, outcome } })
function manifest() { return {
  version: 4, status: 'reviewed', view: { name: 'Notes', fingerprint, steps: [{ schema: 'api', table: 'notes', columns: ['id'], filter: null, sort: null, via: null }] },
  fixtures: [{ name: 'owned', schema: 'api', table: 'notes', keys: { id: 7 } }],
  checks: [{ identity: 'alice', steps: [{ fixture: 'owned', rows: 1 }] }, { identity: 'bob', steps: [{ fixture: 'owned', rows: 0 }] }],
  mutationFixtures: [{ name: 'created', schema: 'api', table: 'notes', keys: { id: 8 } }],
  mutations: [
    check('Denied_create', 'bob', 'POST', 'created', { id: 8 }, 403, 0, 'unchanged'), check('Create', 'alice', 'POST', 'created', { id: 8 }, 201, 1, 'created'),
    check('Denied_update', 'bob', 'PATCH', 'owned', { body: 'no' }, 204, 0, 'unchanged'), check('Update', 'alice', 'PATCH', 'owned', { body: 'yes' }, 204, 1, 'updated'),
    check('Denied_delete', 'bob', 'DELETE', 'owned', null, 204, 0, 'unchanged'), check('Delete', 'alice', 'DELETE', 'owned', null, 204, 1, 'deleted'),
  ],
} }

test('qualification requires reviewed, same-fixture allowed and denied operations by distinct local subjects', () => {
  const result = validateWriteQualification(manifest(), project, scope)
  assert.deepEqual(result, { ...scope, scope: 'disposable_local_database', coverage: 'supplied_scenarios_only', hosted_authorization: 'not_tested' })
  const changes = [
    m => { m.status = 'needs_review' },
    m => { m.mutations = m.mutations.filter(x => x.name !== 'Denied_delete') },
    m => { m.mutations[0].identity = 'alice' },
    m => { m.mutations[0].fixture = 'owned' },
    m => { m.mutationFixtures[0].table = 'other' },
    m => { m.checks.pop() },
  ]
  for (const change of changes) { const value = manifest(); change(value); assert.throws(() => validateWriteQualification(value, project, scope)) }
  assert.throws(() => validateWriteQualification(manifest(), { identities: { alice: { subject: 'same' }, bob: { subject: 'same' } } }, scope))
  assert.throws(() => validateWriteQualification(manifest(), project, { ...scope, operations: ['insert'] }))
})
