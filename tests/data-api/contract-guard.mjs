import assert from 'node:assert/strict'
import { createDataClient } from '../../sdk/data/dist/index.js'

export async function verifyRuntimeContractGuard({ url, token, subject, owner, expectedFingerprint }) {
  const different = (expectedFingerprint[0] === 'a' ? 'b' : 'a') + expectedFingerprint.slice(1)
  const before = (await owner.query('SELECT id,subject,body FROM api.notes ORDER BY id')).rows
  let responses = 0
  const mismatched = createDataClient({ url, accessToken: token, expectedSchemaFingerprint: different, onResponse: () => { responses++ } }).schema('api')
  for (const result of [
    await mismatched.from('notes').select('id').retry(true),
    await mismatched.from('notes').insert({ subject, body: 'guard-must-not-insert' }),
    await mismatched.from('notes').update({ body: 'guard-must-not-update' }).neq('id', 0),
    await mismatched.from('notes').delete().neq('id', 0),
    await mismatched.rpc('create_note_with_tags', { note_body: 'guard-must-not-run-rpc', tag_names: ['guard'] }),
  ]) {
    assert.equal(result.status, 409)
    assert.equal(result.error?.code, 'schema_contract_mismatch')
    assert.equal(result.data, null)
  }
  assert.equal(responses, 5, 'mismatched requests must not be retried')
  assert.deepEqual((await owner.query('SELECT id,subject,body FROM api.notes ORDER BY id')).rows, before)
  const matching = createDataClient({ url, accessToken: token, expectedSchemaFingerprint: expectedFingerprint }).schema('api')
  assert.equal((await matching.from('notes').select('id').limit(1)).error, null)
  const inserted = await matching.from('notes').insert({ subject, body: 'guard-matching-write' }).select('id').single()
  assert.equal(inserted.error, null)
  assert.equal((await matching.from('notes').delete().eq('id', inserted.data.id)).error, null)
  // Guarded OpenAPI exports must not accidentally produce unguarded clients.
  const exported = await fetch(`${url}/__gregale/contract/${expectedFingerprint}/rest/v1/`, { headers: { Authorization: `Bearer ${token}` } })
  const document = await exported.json()
  assert.equal(exported.status, 200, JSON.stringify(document))
  assert.equal(document.basePath, `/__gregale/contract/${expectedFingerprint}/rest/v1`)
}

export async function verifyAcceptedContractRollout({ createServer, config, verify, upstream, ready, olderFingerprint, userA, userB, owner }) {
  const gateway = createServer({ ...config, acceptedContracts: { version: 1, current_fingerprint: config.fingerprint, accepted_fingerprints: [olderFingerprint] } }, verify, upstream, ready, () => {})
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  const url = `http://127.0.0.1:${gateway.address().port}`
  try {
    const older = createDataClient({ url, accessToken: userA.token, expectedSchemaFingerprint: olderFingerprint }).schema('api')
    const current = createDataClient({ url, accessToken: userB.token, expectedSchemaFingerprint: config.fingerprint }).schema('api')
    const first = await older.from('notes').insert({ subject: userA.subject, body: 'older-rollout' }).select('id').single()
    assert.equal(first.error, null)
    const second = await current.from('notes').insert({ subject: userB.subject, body: 'current-rollout' }).select('id').single()
    assert.equal(second.error, null)
    try {
      assert.deepEqual((await current.from('notes').select('id').eq('id', first.data.id)).data, [], 'accepted contracts must preserve RLS')
      const rpc = await older.rpc('create_note_with_tags', { note_body: 'older-rollout-rpc', tag_names: [] })
      assert.equal(rpc.error, null)
      await owner.query("DELETE FROM api.notes WHERE body='older-rollout-rpc'")
      const headers = { Authorization: `Bearer ${userA.token}` }
      const exported = await fetch(`${url}/__gregale/contract/${olderFingerprint}/rest/v1/`, { headers })
      const document = await exported.json()
      assert.equal(exported.status, 200, JSON.stringify(document))
      assert.equal(document.basePath, `/__gregale/contract/${olderFingerprint}/rest/v1`)
      const advertised = await (await fetch(url + '/__gregale/schema', { headers })).json()
      assert.equal(advertised.fingerprint, config.fingerprint, 'acceptance must not advertise the older schema as current')
    } finally {
      await owner.query('DELETE FROM api.notes WHERE id=ANY($1::bigint[])', [[first.data.id, second.data.id]])
    }
  } finally { gateway.closeAllConnections(); await new Promise(resolve => gateway.close(resolve)) }
}
