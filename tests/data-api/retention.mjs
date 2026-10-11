import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'

export async function verifyRetention({ owner, client, root, migrationURL, loginURL, pg, userA }) {
  const { cleanup } = await import(pathToFileURL(join(root, 'migrations/cleanup-receipts.mjs')))
  const keys = [], ids = []
  const old = '2000-01-01T00:00:00.000Z', before = '2001-01-01T00:00:00.000Z'
  const create = async body => {
    const key = randomUUID(); keys.push(key)
    const result = await client.createWithTagsOnce(key, body)
    assert.equal(result.error, null); ids.push(result.data[0].id)
    return { key, body, row: result.data[0] }
  }
  const options = { before, batchSize: 1, maxBatches: 1 }
  let connected = false
  const lock = new pg.Client({ connectionString: migrationURL })
  try {
    const retained = await create('retention-original')
    const extra = await create('retention-extra')
    const boundary = await create('retention-boundary')
    const fresh = await create('retention-fresh')
    await owner.query('UPDATE api.note_create_receipts SET created_at=$1 WHERE request_key=ANY($2::uuid[])', [old, [retained.key, extra.key]])
    await owner.query('UPDATE api.note_create_receipts SET created_at=$1 WHERE request_key=$2', ['2000-01-03T00:00:00.000Z', extra.key])
    await owner.query('UPDATE api.note_create_receipts SET created_at=$1 WHERE request_key=$2', [before, boundary.key])
    const preview = await cleanup(migrationURL, options)
    assert.equal(preview.mode, 'dry-run'); assert.equal(preview.eligible, '2'); assert.equal(preview.deleted, '0')
    assert.equal(preview.remaining, '2')
    assert.deepEqual((await client.createWithTagsOnce(retained.key, retained.body)).data, [retained.row], 'eligibility_must_not_expire_a_retained_receipt')
    await assert.rejects(cleanup(loginURL, { ...options, apply: true }), /Receipt table owner required/)
    await owner.query('ALTER TABLE api.note_create_receipts FORCE ROW LEVEL SECURITY')
    try { await assert.rejects(cleanup(migrationURL, { ...options, apply: true }), /Receipt table owner required/) }
    finally { await owner.query('ALTER TABLE api.note_create_receipts NO FORCE ROW LEVEL SECURITY') }
    await assert.rejects(cleanup(migrationURL, { ...options, before: '9999-01-01T00:00:00.000Z' }), /Future cleanup cutoff refused/)
    // Hold both expired keys as an in-flight replay would. Cleanup must skip
    // them, not wait or delete a receipt while an RPC owns its advisory lock.
    await lock.connect(); connected = true; await lock.query('BEGIN')
    for (const key of [retained.key, extra.key]) await lock.query("SELECT pg_advisory_xact_lock(hashtextextended(jsonb_build_array('gregale.note_create', $1::text, $2::uuid)::text, 0))", [userA.subject, key])
    const skipped = await cleanup(migrationURL, { ...options, apply: true, batchSize: 2 })
    assert.equal(skipped.deleted, '0'); assert.equal(skipped.remaining, '2')
    await lock.query('ROLLBACK')
    // Row locks held by another maintenance transaction must be skipped too.
    await lock.query('BEGIN'); await lock.query('SELECT 1 FROM api.note_create_receipts WHERE request_key=ANY($1::uuid[]) FOR UPDATE', [[retained.key, extra.key]])
    assert.equal((await cleanup(migrationURL, { ...options, apply: true, batchSize: 2 })).deleted, '0')
    await lock.query('ROLLBACK')
    const batch = await cleanup(migrationURL, { ...options, apply: true })
    assert.equal(batch.deleted, '1'); assert.equal(batch.batches, 1); assert.equal(batch.remaining, '1')
    const partial = await create('retention-partial')
    await owner.query('UPDATE api.note_create_receipts SET created_at=$1 WHERE request_key=$2', ['2000-01-02T00:00:00.000Z', partial.key])
    await owner.query(`CREATE FUNCTION api.test_receipt_cleanup_failure() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
      BEGIN IF OLD.response->>'body'='retention-extra' THEN RAISE EXCEPTION 'injected cleanup failure'; END IF; RETURN OLD; END $$;
      CREATE TRIGGER test_receipt_cleanup_failure BEFORE DELETE ON api.note_create_receipts FOR EACH ROW EXECUTE FUNCTION api.test_receipt_cleanup_failure()`)
    try {
      await assert.rejects(cleanup(migrationURL, { ...options, apply: true, maxBatches: 2 }), /injected cleanup failure/)
      assert.equal((await owner.query('SELECT count(*)::int AS n FROM api.note_create_receipts WHERE request_key=$1', [partial.key])).rows[0].n, 0, 'earlier_batch_must_remain_committed')
      assert.equal((await owner.query('SELECT count(*)::int AS n FROM api.note_create_receipts WHERE request_key=$1', [extra.key])).rows[0].n, 1, 'failed_batch_must_roll_back')
    } finally { await owner.query('DROP TRIGGER test_receipt_cleanup_failure ON api.note_create_receipts; DROP FUNCTION api.test_receipt_cleanup_failure()') }
    assert.equal((await cleanup(migrationURL, { ...options, apply: true, maxBatches: 2 })).deleted, '1')
    assert.equal((await cleanup(migrationURL, { ...options, apply: true })).deleted, '0')
    // Purge removes snapshots only: the original note is still present, but
    // reusing its key now creates another logical operation.
    assert.equal((await owner.query('SELECT count(*)::int AS n FROM api.notes WHERE id=$1', [retained.row.id])).rows[0].n, 1)
    const replay = await client.createWithTagsOnce(retained.key, retained.body)
    assert.equal(replay.error, null); ids.push(replay.data[0].id)
    assert.notEqual(replay.data[0].id, retained.row.id)
    assert.equal((await cleanup(migrationURL, options)).eligible, '0', 'new_receipt_must_not_reuse_old_expiry')
    for (const entry of [boundary, fresh]) assert.deepEqual((await client.createWithTagsOnce(entry.key, entry.body)).data, [entry.row])
    assert.equal((await client.remove(boundary.row.id)).error, null)
    assert.deepEqual((await client.createWithTagsOnce(boundary.key, boundary.body)).data, [boundary.row], 'note_deletion_must_not_silently_remove_receipts')
  } finally {
    if (connected) { try { await lock.query('ROLLBACK') } catch {} }
    await lock.end()
    await owner.query('DELETE FROM api.note_create_receipts WHERE request_key=ANY($1::uuid[])', [keys])
    await owner.query('DELETE FROM api.notes WHERE id=ANY($1::int[])', [ids])
  }
}
