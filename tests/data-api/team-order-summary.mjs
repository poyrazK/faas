import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'

export async function verifyTeamOrderSummary({ owner, pg, loginURL, url, typed, teamsClient, token, adminToken, memberToken, otherToken }) {
  const { parseOrderSummary } = await import(pathToFileURL(join(typed, 'dist/order-summary.js')))
  const alice = teamsClient({ url, accessToken: token }, 'shop-a')
  const amy = teamsClient({ url, accessToken: adminToken }, 'shop-a')
  const ann = teamsClient({ url, accessToken: memberToken }, 'shop-a')
  const bob = teamsClient({ url, accessToken: otherToken }, 'shop-b')
  const baseline = (await owner.query("SELECT id,status,total_cents,created_by FROM api.team_orders WHERE tenant_id='shop-a'")).rows
  const fixture = (await owner.query(`INSERT INTO api.team_orders(id,tenant_id,created_by,total_cents,status)
    SELECT gen_random_uuid(),'shop-a','summary-fixture',CASE WHEN n%5=0 THEN 2147483647 ELSE n%5*100 END,
      (ARRAY['draft','submitted','fulfilled']::api.order_status[])[n%3+1] FROM generate_series(1,2501) n
    RETURNING id,status,total_cents,created_by`)).rows
  const hiddenID = randomUUID(), foreignID = randomUUID(), ids = [...fixture.map(row => row.id), hiddenID]
  const hidden = { id: hiddenID, status: 'draft', total_cents: 2147483647, created_by: 'summary-hidden' }
  await owner.query(`INSERT INTO api.team_orders(id,tenant_id,created_by,total_cents,status) VALUES
    ($1,'shop-a','summary-hidden',2147483647,'draft'),($2,'shop-b','summary-foreign',2147483647,'fulfilled')`, [hiddenID, foreignID])
  let rows = [...baseline, ...fixture, hidden]
  const expected = (filters = {}) => {
    const buckets = ['draft', 'submitted', 'fulfilled'].map(status => ({ status, order_count: '0', total_cents: '0' }))
    for (const row of rows) {
      if (filters.status != null && filters.status !== row.status || filters.minimum != null && row.total_cents < filters.minimum || filters.maximum != null && row.total_cents > filters.maximum) continue
      const bucket = buckets.find(bucket => bucket.status === row.status)
      bucket.order_count = String(BigInt(bucket.order_count) + 1n); bucket.total_cents = String(BigInt(bucket.total_cents) + BigInt(row.total_cents))
    }
    return { tenant_id: 'shop-a', order_count: String(buckets.reduce((n, bucket) => n + BigInt(bucket.order_count), 0n)),
      total_cents: String(buckets.reduce((n, bucket) => n + BigInt(bucket.total_cents), 0n)), by_status: buckets }
  }
  const read = async (client = alice, filters = {}) => {
    const response = await client.summary(filters)
    assert.equal(response.error, null, JSON.stringify(response.error)); assert.equal(response.data.length, 1)
    return parseOrderSummary(response.data[0], client.tenant, filters)
  }
  const fail = (response, status, message) => { assert.equal(response.status, status, JSON.stringify(response.error)); if (message) assert.equal(response.error.message, message) }
  const direct = new pg.Client({ connectionString: loginURL }); await direct.connect()
  try {
    assert.deepEqual(await read(), expected())
    assert.deepEqual(await read(amy), expected()); assert.deepEqual(await read(ann), expected())
    assert.ok(BigInt((await read()).total_cents) > 2147483647n)
    const capped = await alice.db.from('team_orders').select('id').eq('tenant_id', 'shop-a').limit(10000).retry(false)
    assert.equal(capped.error, null); assert.equal(capped.data.length, 1000)
    assert.ok(BigInt((await read()).order_count) > BigInt(capped.data.length))
    for (const filters of [{ status: 'submitted' }, { minimum: 100, maximum: 100 }, { minimum: 2147483647 },
      { maximum: 0 }, { minimum: 401, maximum: 401 }, { status: 'fulfilled', minimum: 100, maximum: 400 },
      { status: null, minimum: null, maximum: null }]) assert.deepEqual(await read(alice, filters), expected(filters))
    assert.equal((await read(bob)).by_status[2].total_cents, '2147483647')
    fail(await bob.db.rpc('summarize_team_orders', { team_id: 'shop-a' }).retry(false), 403, 'team_membership_required')
    fail(await alice.db.rpc('summarize_team_orders', { team_id: 'shop-b' }).retry(false), 403, 'team_membership_required')
    fail(await alice.db.rpc('summarize_team_orders', { team_id: 'nonexistent-team' }).retry(false), 403, 'team_membership_required')

    // Order-table visibility is still authoritative after the membership check.
    await owner.query("CREATE POLICY summary_current_visibility ON api.team_orders AS RESTRICTIVE FOR SELECT USING(created_by<>'summary-hidden')")
    try { rows = rows.filter(row => row.id !== hiddenID); assert.deepEqual(await read(), expected()) }
    finally { await owner.query('DROP POLICY summary_current_visibility ON api.team_orders'); rows.push(hidden) }

    // An unchanged JWT loses access on the next statement after revocation.
    await owner.query("DELETE FROM api.team_members WHERE tenant_id='shop-a' AND subject='ann'")
    try { fail(await ann.summary(), 403, 'team_membership_required'); assert.deepEqual(await read(), expected()) }
    finally { await owner.query("INSERT INTO api.team_members(tenant_id,subject,role) VALUES('shop-a','ann','member')") }
    assert.deepEqual(await read(ann), expected())
    await owner.query("UPDATE api.team_members SET role='member' WHERE tenant_id='shop-a' AND subject='amy'")
    try {
      assert.deepEqual(await read(amy), expected())
      fail(await amy.db.rpc('save_team_order_once', { request_key: randomUUID(), team_id: 'shop-a', order_id: randomUUID(), expected_version: 0, order_total: 1, order_state: 'draft' }).retry(false), 403, 'team_write_denied')
    } finally { await owner.query("UPDATE api.team_members SET role='admin' WHERE tenant_id='shop-a' AND subject='amy'") }

    const edit = fixture.find(row => row.status === 'draft')
    const changed = await alice.db.rpc('save_team_order_once', { request_key: randomUUID(), team_id: 'shop-a', order_id: edit.id, expected_version: 1, order_total: 12345, order_state: 'draft' }).retry(false)
    assert.equal(changed.error, null); edit.total_cents = 12345
    assert.deepEqual(await read(), expected())
    await owner.query('DELETE FROM api.team_orders WHERE id=$1', [edit.id]); rows = rows.filter(row => row.id !== edit.id)
    assert.deepEqual(await read(), expected(), 'summary reads current data after edits and deletes')

    for (const args of [{ team_id: null }, { team_id: '' }, { team_id: 'shop-a', minimum_total: -1 },
      { team_id: 'shop-a', maximum_total: -1 }, { team_id: 'shop-a', minimum_total: 2, maximum_total: 1 }]) {
      fail(await alice.db.rpc('summarize_team_orders', args).retry(false), 400, 'invalid_order_summary')
    }
    for (const args of [{ team_id: 'shop-a', status_filter: 'paid' }, { team_id: 'shop-a', minimum_total: 2147483648 }, { team_id: 'shop-a', maximum_total: 1.5 }]) {
      fail(await alice.db.rpc('summarize_team_orders', args).retry(false), 400)
    }
    await direct.query("SELECT set_config('request.jwt.claims','{\"sub\":\"ann\"}',false)")
    assert.deepEqual((await direct.query("SELECT rolsuper,rolbypassrls,row_security_active('api.team_orders') AS active FROM pg_roles WHERE rolname=current_user")).rows[0], { rolsuper: false, rolbypassrls: false, active: true })
    await direct.query('BEGIN READ ONLY')
    assert.deepEqual((await direct.query("SELECT * FROM api.summarize_team_orders('shop-a')")).rows[0], expected())
    await direct.query('ROLLBACK')
    await direct.query("SELECT set_config('request.jwt.claims','{}',false)")
    await assert.rejects(direct.query("SELECT * FROM api.summarize_team_orders('shop-a')"), error => error.code === '42501')
    const document = await fetch(url + '/openapi.json', { headers: { Authorization: `Bearer ${token}` } })
    assert.equal(document.status, 200)
    const spec = await document.json(); assert.ok(spec.paths['/rpc/summarize_team_orders'].post); assert.equal(spec.paths['/rpc/summarize_team_orders'].get, undefined)
  } finally {
    await direct.query('ROLLBACK'); await direct.end()
    await owner.query('DELETE FROM api.team_order_events WHERE order_id=ANY($1::uuid[])', [ids])
    await owner.query("DELETE FROM api.team_order_receipts WHERE payload->>'id'=ANY($1::text[])", [ids])
    await owner.query('DELETE FROM api.team_orders WHERE id=ANY($1::uuid[])', [[...ids, foreignID]])
  }
}
