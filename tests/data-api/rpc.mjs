import assert from 'node:assert/strict'

export async function verifyRPC({ owner, client, url, userA, userB }) {
  const ids = [], tags = []
  try {
    const created = await client.createWithTags('rpc-owned', ['first', 'second'])
    assert.equal(created.error, null)
    assert.equal(created.data.length, 1)
    const note = created.data[0]
    ids.push(note.id)
    assert.equal(note.subject, userA.subject)
    assert.equal(note.version, 1)
    const attached = await owner.query('SELECT t.id, t.subject, t.name FROM api.tags t JOIN api.note_tags nt ON nt.subject=t.subject AND nt.tag_id=t.id WHERE nt.note_id=$1 ORDER BY t.name', [note.id])
    tags.push(...attached.rows.map(row => row.id))
    assert.deepEqual(attached.rows.map(row => row.name), ['first', 'second'])
    assert.ok(attached.rows.every(row => row.subject === userA.subject))
    const foreign = await fetch(`${url}/rest/v1/notes?id=eq.${note.id}`, { headers: { Authorization: `Bearer ${userB.token}` } })
    assert.deepEqual(await foreign.json(), [])
    const defaults = await client.createWithTags('rpc-default')
    assert.equal(defaults.error, null)
    ids.push(defaults.data[0].id)
    const failed = await client.createWithTags('rpc-rollback', ['partial', null])
    assert.equal(failed.error.code, '23502')
    assert.equal((await owner.query("SELECT count(*)::int AS n FROM api.notes WHERE body='rpc-rollback'")).rows[0].n, 0)
    assert.equal((await owner.query("SELECT count(*)::int AS n FROM api.tags WHERE name='partial'")).rows[0].n, 0)
    await owner.query("CREATE POLICY rpc_deny_tag ON api.tags AS RESTRICTIVE FOR INSERT WITH CHECK (name <> 'blocked')")
    try {
      const denied = await client.createWithTags('rpc-rls-rollback', ['partial', 'blocked'])
      assert.equal(denied.error.code, '42501')
      assert.equal((await owner.query("SELECT count(*)::int AS n FROM api.notes WHERE body='rpc-rls-rollback'")).rows[0].n, 0)
      assert.equal((await owner.query("SELECT count(*)::int AS n FROM api.tags WHERE name='partial'")).rows[0].n, 0)
    } finally { await owner.query('DROP POLICY rpc_deny_tag ON api.tags') }
    const forged = await client.db.rpc('create_note_with_tags', { note_body: 'rpc-forged', subject: userB.subject }).retry(false)
    assert.equal(forged.error.code, 'PGRST202')
    assert.equal((await fetch(`${url}/rest/v1/rpc/create_note_with_tags`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' })).status, 401)
    assert.equal((await fetch(`${url}/rest/v1/rpc/create_note_with_tags`, { headers: { Authorization: `Bearer ${userA.token}` } })).status, 405)
    const spec = await (await fetch(`${url}/openapi.json`, { headers: { Authorization: `Bearer ${userA.token}` } })).json()
    assert.ok(spec.paths['/rpc/create_note_with_tags'].post)
    assert.equal(spec.paths['/rpc/create_note_with_tags'].get, undefined)
    assert.ok(!Object.keys(spec.paths).some(path => path.includes('advance_note_version')))
  } finally {
    await owner.query('DELETE FROM api.notes WHERE id=ANY($1::int[])', [ids])
    await owner.query('DELETE FROM api.tags WHERE id=ANY($1::int[])', [tags])
  }
}

export async function verifyRPCPolicy({ owner, inspect, loginURL, role }) {
  await owner.query(`
    CREATE FUNCTION api.rpc_plain(value text) RETURNS text LANGUAGE sql AS 'SELECT value';
    CREATE FUNCTION api.rpc_definer(value text) RETURNS text LANGUAGE sql SECURITY DEFINER AS 'SELECT value';
    COMMENT ON FUNCTION api.rpc_definer(text) IS '@gregale:rpc';
    CREATE FUNCTION api.rpc_overloaded(value text) RETURNS text LANGUAGE sql AS 'SELECT value';
    CREATE FUNCTION api.rpc_overloaded(value integer) RETURNS integer LANGUAGE sql AS 'SELECT value';
    COMMENT ON FUNCTION api.rpc_overloaded(text) IS '@gregale:rpc';
    CREATE FUNCTION api.rpc_positional(text) RETURNS text LANGUAGE sql AS 'SELECT $1';
    COMMENT ON FUNCTION api.rpc_positional(text) IS '@gregale:rpc';
    CREATE FUNCTION api.rpc_private(value text) RETURNS text LANGUAGE sql AS 'SELECT value';
    REVOKE ALL ON FUNCTION api.rpc_private(text) FROM PUBLIC;
    COMMENT ON FUNCTION api.rpc_private(text) IS '@gregale:rpc';
    CREATE FUNCTION api.rpc_scalar(value text DEFAULT 'default') RETURNS text LANGUAGE sql AS 'SELECT value';
    COMMENT ON FUNCTION api.rpc_scalar(text) IS '@gregale:rpc';
    CREATE FUNCTION api.rpc_empty() RETURNS boolean LANGUAGE sql AS 'SELECT true';
    COMMENT ON FUNCTION api.rpc_empty() IS '@gregale:rpc';
  `)
  try {
    const functions = (await inspect(loginURL, ['api'])).functions
    assert.deepEqual(functions.map(f => f.name), ['create_note_with_tags', 'rpc_empty', 'rpc_scalar'])
    assert.deepEqual(functions.find(f => f.name === 'rpc_scalar').args, [{ name: 'value', type: 'string', optional: true }])
    assert.equal(functions.find(f => f.name === 'rpc_scalar').returns, 'string | null')
    assert.equal(functions.find(f => f.name === 'rpc_empty').returns, 'boolean | null')
    await owner.query(`REVOKE EXECUTE ON FUNCTION api.create_note_with_tags(text, text[]) FROM "${role}"`)
    assert.ok(!(await inspect(loginURL, ['api'])).functions.some(f => f.name === 'create_note_with_tags'))
  } finally {
    await owner.query(`GRANT EXECUTE ON FUNCTION api.create_note_with_tags(text, text[]) TO "${role}";
      DROP FUNCTION api.rpc_plain(text), api.rpc_definer(text), api.rpc_overloaded(text), api.rpc_overloaded(integer), api.rpc_positional(text), api.rpc_private(text), api.rpc_scalar(text), api.rpc_empty();`)
  }
}
