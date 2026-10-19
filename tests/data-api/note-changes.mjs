import assert from 'node:assert/strict'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'

export async function verifyNoteChanges({ owner, client, root, pg, loginURL, userA, userB, makeClient, url }) {
  const { parseNoteSyncCheckpoint, parseNoteChangePage, noteChangeCursorExpired, NOTE_CHANGE_FEED_LIMITS } = await import(pathToFileURL(join(root, 'client/dist/note-changes.js')))
  const prefix = `feed-v1-${Date.now()}-`
  const peers = []
  const connect = async subject => {
    const peer = new pg.Client({ connectionString: loginURL, query_timeout: 15000 }); await peer.connect(); peers.push(peer)
    await peer.query("SELECT set_config('request.jwt.claims',$1,false)", [JSON.stringify({ sub: subject })]); return peer
  }
  const checkpoint = async () => {
    const result = await client.startSync(); assert.equal(result.error, null)
    return parseNoteSyncCheckpoint(result.data[0])
  }
  const read = async (cursor, size = 100) => {
    const result = await client.readChanges(cursor, size); assert.equal(result.error, null, JSON.stringify(result.error))
    assert.equal(result.data.length, 1); return parseNoteChangePage(result.data[0], cursor)
  }
  const create = async suffix => { const result = await client.create(prefix + suffix); assert.equal(result.error, null); return result.data }
  const failure = (result, status, message) => { assert.equal(result.status, status, JSON.stringify(result.error)); assert.equal(result.error.message, message) }
  const fingerprint = async () => (await owner.query('SELECT head::text AS head,discarded_through::text AS floor FROM api.note_change_streams WHERE subject=$1', [userA.subject])).rows
  try {
    // Public HTTP flow includes a deletion marker after its note has disappeared.
    const start = await checkpoint(), note = await create('lifecycle')
    assert.equal((await client.update(note.id, 1, { body: prefix + 'updated' })).error, null)
    assert.equal((await client.remove(note.id)).error, null)
    const first = await read(start, 2); assert.equal(first.has_more, true)
    const second = await read(first.next_cursor, 2); assert.equal(second.has_more, false)
    const events = [...first.changes, ...second.changes]
    assert.deepEqual(events.map(e => [e.operation, e.note_id, e.version]), [['insert', note.id, 1], ['update', note.id, 2], ['delete', note.id, 2]])
    assert.deepEqual((await read(second.next_cursor)).changes, [])
    assert.doesNotMatch(JSON.stringify(events), /body|subject|payload|created_at/)

    const bob = makeClient({ url, subject: userB.subject, accessToken: userB.token })
    failure(await bob.db.rpc('read_note_changes', { after_cursor: start }).retry(false), 400, 'note_change_cursor_invalid')
    for (const cursor of [null, '', start + '\n', start.replace(/\.\d+$/, '.01'), start.replace(/\.\d+$/, '.9223372036854775808'), start.replace(/\.\d+$/, '.9223372036854775807'), start.toUpperCase()]) {
      failure(await client.db.rpc('read_note_changes', { after_cursor: cursor }).retry(false), 400, 'note_change_cursor_invalid')
    }
    for (const size of [null, 0, -1, 101]) failure(await client.db.rpc('read_note_changes', { after_cursor: start, page_size: size }).retry(false), 400, 'note_change_page_size_invalid')
    for (const table of ['note_change_feed', 'note_change_streams', 'note_changes']) {
      const hidden = await client.db.from(table).select().retry(false); assert.equal(hidden.error, null); assert.deepEqual(hidden.data, [])
    }
    const forged = await client.db.from('note_changes').insert({ subject: userA.subject, position: 9000, note_id: 1, operation: 'delete', version: 1 }).retry(false)
    assert.equal(forged.error.code, '42501')
    const streamForged = await client.db.from('note_change_streams').insert({ subject: userA.subject, head: 1, discarded_through: 0 }).retry(false)
    assert.equal(streamForged.error.code, '42501')

    // Both readers work in READ ONLY, including a subject with no stream row.
    const direct = await connect(userA.subject)
    await direct.query('BEGIN READ ONLY'); await direct.query("SELECT set_config('gregale.note_change_scope','before-feed-read',true)")
    assert.equal((await direct.query('SELECT * FROM api.read_note_changes($1,100)', [start])).rows[0].changes.length, 3)
    assert.ok((await direct.query('SELECT * FROM api.start_note_sync()')).rows[0].cursor)
    assert.equal((await direct.query("SELECT current_setting('gregale.note_change_scope',true) AS scope")).rows[0].scope, 'before-feed-read')
    assert.deepEqual((await direct.query('SELECT * FROM api.note_changes')).rows, [])
    await direct.query('ROLLBACK')
    const emptyActor = prefix + 'empty', empty = await connect(emptyActor)
    await empty.query('BEGIN READ ONLY')
    const emptyCursor = (await empty.query('SELECT * FROM api.start_note_sync()')).rows[0].cursor
    assert.deepEqual((await empty.query('SELECT * FROM api.read_note_changes($1,100)', [emptyCursor])).rows[0].changes, [])
    await empty.query('ROLLBACK')
    assert.equal((await owner.query('SELECT count(*)::int AS n FROM api.note_change_streams WHERE subject=$1', [emptyActor])).rows[0].n, 0)

    // A failure after capture rolls back the business row, head and event.
    await owner.query(`CREATE FUNCTION api.reject_after_feed() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
      IF EXISTS(SELECT 1 FROM feed_failure_new WHERE body='${prefix}late-failure') THEN RAISE EXCEPTION 'feed_late_failure'; END IF; RETURN NULL; END $$;
      CREATE TRIGGER zz_reject_after_feed AFTER INSERT ON api.notes REFERENCING NEW TABLE AS feed_failure_new
        FOR EACH STATEMENT EXECUTE FUNCTION api.reject_after_feed()`)
    try {
      const before = await fingerprint(), cursor = await checkpoint()
      const result = await client.create(prefix + 'late-failure'); assert.equal(result.error.message, 'feed_late_failure')
      assert.deepEqual(await fingerprint(), before); assert.deepEqual((await read(cursor)).changes, [])
      assert.equal((await owner.query('SELECT count(*)::int AS n FROM api.notes WHERE body=$1', [prefix + 'late-failure'])).rows[0].n, 0)
    } finally { await owner.query('DROP TRIGGER zz_reject_after_feed ON api.notes; DROP FUNCTION api.reject_after_feed()') }

    // Allocate under the per-subject row lock, not a sequence/transaction id.
    // Exercise both the first-ever head insert and a subsequent aborted writer.
    const actor = prefix + 'concurrent', writer = await connect(actor), waiter = await connect(actor), reader = await connect(actor), other = await connect(prefix + 'independent')
    const writerPID = (await writer.query('SELECT pg_backend_pid() AS pid')).rows[0].pid
    const waiterPID = (await waiter.query('SELECT pg_backend_pid() AS pid')).rows[0].pid
    for (const commit of [true, false]) {
      const cursor = (await reader.query('SELECT * FROM api.start_note_sync()')).rows[0].cursor
      await writer.query('BEGIN'); await waiter.query('BEGIN')
      const firstNote = (await writer.query('INSERT INTO api.notes(subject,body) VALUES($1,$2) RETURNING id', [actor, prefix + 'first-writer'])).rows[0]
      const pending = waiter.query('INSERT INTO api.notes(subject,body) VALUES($1,$2) RETURNING id', [actor, prefix + 'second-writer']).then(result => ({ result }), error => ({ error }))
      try {
        let blocked = false
        for (let i = 0; i < 100; i++) {
          blocked = (await owner.query('SELECT $1=ANY(pg_blocking_pids($2)) AS blocked', [writerPID, waiterPID])).rows[0].blocked
          if (blocked) break
          await new Promise(resolve => setTimeout(resolve, 10))
        }
        assert.equal(blocked, true, 'same-subject writer must actually wait for the uncommitted stream head')
        await other.query('INSERT INTO api.notes(subject,body) VALUES($1,$2)', [prefix + 'independent', prefix + 'independent'])
        assert.deepEqual((await reader.query('SELECT * FROM api.read_note_changes($1,100)', [cursor])).rows[0].changes, [], 'uncommitted writes must not be observed or skipped')
        await writer.query(commit ? 'COMMIT' : 'ROLLBACK')
        const completed = await pending; assert.equal(completed.error, undefined)
        const visible = (await reader.query('SELECT * FROM api.read_note_changes($1,100)', [cursor])).rows[0]
        assert.deepEqual(visible.changes.map(e => e.note_id), commit ? [firstNote.id] : [])
        await waiter.query('COMMIT')
        const final = parseNoteChangePage((await reader.query('SELECT * FROM api.read_note_changes($1,100)', [cursor])).rows[0], cursor)
        assert.deepEqual(final.changes.map(e => e.note_id), commit ? [firstNote.id, completed.result.rows[0].id] : [completed.result.rows[0].id])
      } finally { await writer.query('ROLLBACK'); await pending; await waiter.query('ROLLBACK') }
    }

    // Owner reassignment invalidates the old subject and the new subject.
    const moved = await create('owner-move'), a = await checkpoint(), b = parseNoteSyncCheckpoint((await bob.startSync()).data[0])
    await owner.query('UPDATE api.notes SET subject=$1 WHERE id=$2', [userB.subject, moved.id])
    assert.deepEqual((await read(a)).changes.map(e => [e.operation, e.note_id]), [['delete', moved.id]])
    const movedPage = await bob.readChanges(b)
    assert.deepEqual(parseNoteChangePage(movedPage.data[0], b).changes.map(e => [e.operation, e.note_id]), [['insert', moved.id]])

    // A checkpoint before a live row scan closes the bootstrap race. Re-fetch
    // every affected id, including tombstones, so a newer row is never replaced
    // by a historical image. Only persist a page cursor after applying it.
    const baseline = await create('bootstrap-update'), deleted = await create('bootstrap-delete'), bootstrap = await checkpoint()
    const cache = new Map(), firstRows = await client.syncRows({ size: 2 }); assert.equal(firstRows.error, null)
    for (const row of firstRows.data) cache.set(row.id, row)
    await client.update(baseline.id, 1, { body: prefix + 'bootstrap-new-value' }); await client.remove(deleted.id); await create('bootstrap-insert')
    let afterID = firstRows.data.at(-1)?.id ?? 0
    for (;;) {
      const result = await client.syncRows({ afterID, size: 2 }); assert.equal(result.error, null)
      if (!result.data.length) break
      for (const row of result.data) cache.set(row.id, row)
      afterID = result.data.at(-1).id
    }
    let cursor = bootstrap
    for (;;) {
      const page = await read(cursor, 2), ids = [...new Set(page.changes.map(e => e.note_id))]
      if (ids.length) {
        const current = await client.db.from('notes').select('id,body,priority,created_at,version').in('id', ids).retry(false); assert.equal(current.error, null)
        const present = new Map(current.data.map(row => [row.id, row]))
        for (const id of ids) { if (present.has(id)) cache.set(id, present.get(id)); else cache.delete(id) }
      }
      cursor = page.next_cursor
      if (!page.has_more) break
    }
    const expected = (await owner.query('SELECT id,body,version FROM api.notes WHERE subject=$1 ORDER BY id', [userA.subject])).rows
    assert.deepEqual([...cache.values()].map(({ id, body, version }) => ({ id, body, version })).sort((a, b) => a.id - b.id), expected)

    // Retention advances the watermark atomically with its bounded suffix.
    const old = await checkpoint()
    await owner.query('INSERT INTO api.notes(subject,body) SELECT $1,$2 FROM generate_series(1,$3)', [userA.subject, prefix + 'retention', NOTE_CHANGE_FEED_LIMITS.retainedEvents + 2])
    const expired = await client.readChanges(old); failure(expired, 410, 'note_change_cursor_expired'); assert.equal(noteChangeCursorExpired(expired), true)
    const saved = (await owner.query('SELECT head::text AS head,discarded_through::text AS floor FROM api.note_change_streams WHERE subject=$1', [userA.subject])).rows[0]
    assert.equal((await owner.query('SELECT count(*)::int AS n FROM api.note_changes WHERE subject=$1', [userA.subject])).rows[0].n, NOTE_CHANGE_FEED_LIMITS.retainedEvents)
    const floorCursor = old.replace(/\.\d+$/, '.' + saved.floor), retained = await read(floorCursor, 1)
    assert.equal(retained.changes[0].sequence, String(BigInt(saved.floor) + 1n)); assert.equal(retained.has_more, true)
    const fresh = await checkpoint(); assert.deepEqual((await read(fresh)).changes, [])
    const renewed = await create('retention-after')
    assert.deepEqual((await read(fresh)).changes.map(e => e.note_id), [renewed.id], 'serving-role capture must also prune the expired prefix')
    await assert.rejects(owner.query('DELETE FROM api.note_change_streams WHERE subject=$1', [userA.subject]), error => error.message === 'note_change_stream_managed')
    await assert.rejects(owner.query('DELETE FROM api.note_changes WHERE subject=$1', [userA.subject]), error => error.message === 'note_change_event_managed')
  } finally {
    for (const peer of peers) { await peer.query('ROLLBACK').catch(() => {}); await peer.end() }
    await owner.query('DELETE FROM api.notes WHERE body LIKE $1', [prefix + '%'])
  }
}
