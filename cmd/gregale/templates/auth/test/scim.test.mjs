import test from 'node:test'
import assert from 'node:assert/strict'
import { ScimError, applyPatch, parseFilter, scimRoute, userInput, userResource } from '../scim.mjs'

const provider = '6a1b2c3d-0000-4000-8000-000000000001'
const base = 'https://shop-auth.gregale.app'

test('a SCIM User keeps the attributes Gregale needs', () => {
  assert.deepEqual(userInput({ userName: ' ada@acme.com ', externalId: 'okta-1', name: { givenName: 'Ada', familyName: 'Lovelace' }, displayName: 'Ada L', emails: [{ value: 'ada@work.acme.com' }, { value: 'ada@acme.com', primary: true }] }),
    { user_name: 'ada@acme.com', external_id: 'okta-1', email: 'ada@acme.com', display_name: 'Ada L', given_name: 'Ada', family_name: 'Lovelace', active: true })
  assert.equal(userInput({ userName: 'x', active: 'False' }).active, false)
  assert.throws(() => userInput({}), ScimError)
  assert.throws(() => userInput({ userName: 'x', active: 'maybe' }), ScimError)
})

test('patches in the Okta and Entra ID forms', () => {
  const row = { user_name: 'ada@acme.com', email: 'ada@acme.com', active: true, given_name: null, family_name: null, display_name: null, external_id: 'e1' }
  assert.equal(applyPatch(row, { Operations: [{ op: 'replace', value: { active: false } }] }).active, false)
  assert.equal(applyPatch(row, { Operations: [{ op: 'Replace', path: 'active', value: 'False' }] }).active, false)
  const renamed = applyPatch(row, { Operations: [{ op: 'Replace', path: 'name.givenName', value: 'Ada' }, { op: 'Add', path: 'emails[type eq "work"].value', value: 'ada2@acme.com' }, { op: 'replace', path: 'urn:ietf:params:scim:schemas:extension:enterprise:2.0:User:department', value: 'R&D' }] })
  assert.equal(renamed.given_name, 'Ada')
  assert.equal(renamed.email, 'ada2@acme.com')
  assert.throws(() => applyPatch(row, { Operations: [{ op: 'remove', path: 'active' }] }), { scimType: 'invalidSyntax' })
  assert.throws(() => applyPatch(row, {}), ScimError)
})

test('filters find a person by userName or externalId', () => {
  assert.equal(parseFilter(null), null)
  assert.deepEqual(parseFilter('userName eq "ada@acme.com"'), { attribute: 'user_name', value: 'ada@acme.com' })
  assert.deepEqual(parseFilter('externalId eq "a\\"b"'), { attribute: 'external_id', value: 'a"b' })
  assert.deepEqual(parseFilter('USERNAME EQ "x"'), { attribute: 'user_name', value: 'x' })
  for (const bad of ['userName sw "a"', 'emails eq "a"', 'userName eq "a" or 1 eq 1', 'userName eq a']) assert.throws(() => parseFilter(bad), { scimType: 'invalidFilter' }, bad)
})

// fakeStore models gregale_auth.scim_users and the effects scim.mjs has on
// GoTrue users, sessions and memberships.
function fakeStore() {
  const state = { rows: [], effects: [], identities: [{ user: 'u-early', email: 'early@acme.com' }] }
  const now = new Date('2026-10-17T10:00:00Z')
  const pool = {
    async query(sql, args = []) {
      if (sql.startsWith('SELECT u.id FROM auth.users')) {
        const hit = state.identities.find(i => i.email === args[1].toLowerCase() && !state.rows.some(r => r.user_id === i.user))
        return { rows: hit ? [{ id: hit.user }] : [] }
      }
      if (sql.startsWith('INSERT INTO gregale_auth.scim_users')) {
        const [provider_id, external_id, user_name, email, display_name, given_name, family_name, active, user_id] = args
        if (state.rows.some(r => r.user_name.toLowerCase() === user_name.toLowerCase())) throw Object.assign(new Error('dup'), { code: '23505' })
        const row = { id: `00000000-0000-4000-8000-00000000000${state.rows.length + 1}`, provider_id, external_id, user_name, email, display_name, given_name, family_name, active, user_id, created_at: now, updated_at: now }
        state.rows.push(row)
        return { rows: [row] }
      }
      if (sql.includes('LIMIT $4 OFFSET $5')) {
        const [, value, attribute, limit, offset] = args
        const hits = state.rows.filter(r => !attribute || (attribute === 'user_name' ? r.user_name.toLowerCase() === value.toLowerCase() : r.external_id === value))
        return { rows: hits.slice(offset, offset + limit) }
      }
      if (sql.includes('count(*)::int AS total')) {
        const [, value, attribute] = args
        return { rows: [{ total: state.rows.filter(r => !attribute || (attribute === 'user_name' ? r.user_name.toLowerCase() === value.toLowerCase() : r.external_id === value)).length }] }
      }
      if (sql.startsWith('SELECT id, provider_id')) return { rows: state.rows.filter(r => r.provider_id === args[0] && r.id === args[1]).map(r => ({ ...r })) }
      if (sql.startsWith('UPDATE gregale_auth.scim_users')) {
        const row = state.rows.find(r => r.id === args[0])
        Object.assign(row, { external_id: args[1], user_name: args[2], email: args[3], display_name: args[4], given_name: args[5], family_name: args[6], active: args[7] })
        return { rows: [{ ...row }] }
      }
      if (sql.startsWith('DELETE FROM gregale_auth.scim_users')) { state.rows = state.rows.filter(r => r.id !== args[0]); return { rows: [] } }
      if (sql.startsWith('DELETE FROM auth.sessions')) { state.effects.push(`end sessions ${args[0]}`); return { rows: [] } }
      if (sql.startsWith('DELETE FROM gregale_auth.memberships')) { state.effects.push(`leave org ${args[1]}`); return { rows: [] } }
      if (sql.startsWith('INSERT INTO gregale_auth.memberships')) { state.effects.push(`join org ${args[1]}`); return { rows: [] } }
      throw new Error('unexpected SQL ' + sql)
    },
  }
  const admin = async (method, path, body) => { state.effects.push(`${method} ${path} ${body.ban_duration}`); return { status: 200, data: {} } }
  const route = (method, path, body) => scimRoute({ pool, admin, base }, provider, method, new URL(path, base), body)
  return { state, route }
}

test('provisioning, lookup and deprovisioning follow the identity provider', async () => {
  const { state, route } = fakeStore()
  const [created, user] = await route('POST', '/scim/v2/Users', { userName: 'Ada@acme.com', externalId: 'okta-1', emails: [{ value: 'ada@acme.com', primary: true }] })
  assert.equal(created, 201)
  assert.equal(user.meta.location, `${base}/scim/v2/Users/${user.id}`)
  assert.equal(user.active, true)
  assert.deepEqual(state.effects, [], 'an unlinked record touches no GoTrue user')
  await assert.rejects(route('POST', '/scim/v2/Users', { userName: 'ada@ACME.com' }), { status: 409, scimType: 'uniqueness' })

  const [, found] = await route('GET', '/scim/v2/Users?filter=' + encodeURIComponent('userName eq "ADA@acme.com"'))
  assert.deepEqual([found.totalResults, found.Resources[0].id], [1, user.id])
  const [, none] = await route('GET', '/scim/v2/Users?filter=' + encodeURIComponent('externalId eq "nobody"'))
  assert.equal(none.totalResults, 0)

  // A person who signed in before being provisioned is linked at once.
  const [, early] = await route('POST', '/scim/v2/Users', { userName: 'early@acme.com' })
  assert.equal(state.rows.find(r => r.id === early.id).user_id, 'u-early')
  assert.deepEqual(state.effects, ['PUT /admin/users/u-early none', 'join org u-early'])
  state.effects.length = 0

  // Deactivation bans the linked person, ends sessions and removes them
  // from the provider's organization; reactivation reverses it.
  await route('PATCH', `/scim/v2/Users/${early.id}`, { schemas: ['urn:ietf:params:scim:api:messages:2.0:PatchOp'], Operations: [{ op: 'Replace', path: 'active', value: 'False' }] })
  assert.deepEqual(state.effects, ['PUT /admin/users/u-early 876000h', 'end sessions u-early', 'leave org u-early'])
  state.effects.length = 0
  await route('PATCH', `/scim/v2/Users/${early.id}`, { Operations: [{ op: 'replace', path: 'displayName', value: 'Early' }] })
  assert.deepEqual(state.effects, [], 'a change that keeps the person active touches nothing')
  await route('PUT', `/scim/v2/Users/${early.id}`, { userName: 'early@acme.com', active: true })
  assert.deepEqual(state.effects, ['PUT /admin/users/u-early none', 'join org u-early'])
  state.effects.length = 0
  assert.deepEqual(await route('DELETE', `/scim/v2/Users/${early.id}`), [204, undefined])
  assert.deepEqual(state.effects, ['PUT /admin/users/u-early 876000h', 'end sessions u-early', 'leave org u-early'])
  await assert.rejects(route('GET', `/scim/v2/Users/${early.id}`), { status: 404 })
  await assert.rejects(route('GET', '/scim/v2/Users/not-a-uuid'), { status: 404 })
  await assert.rejects(route('GET', '/scim/v2/Groups'), { status: 404 })
  assert.equal((await route('GET', '/scim/v2/ServiceProviderConfig'))[1].patch.supported, true)
})

test('resources render as SCIM Users', () => {
  const at = new Date('2026-10-17T10:00:00Z')
  const resource = userResource({ id: 'i1', external_id: null, user_name: 'ada@acme.com', email: null, display_name: null, given_name: 'Ada', family_name: null, active: false, created_at: at, updated_at: at }, base)
  assert.deepEqual(resource, { schemas: ['urn:ietf:params:scim:schemas:core:2.0:User'], id: 'i1', userName: 'ada@acme.com', name: { givenName: 'Ada' }, emails: [], active: false,
    meta: { resourceType: 'User', created: '2026-10-17T10:00:00.000Z', lastModified: '2026-10-17T10:00:00.000Z', location: `${base}/scim/v2/Users/i1` } })
})
