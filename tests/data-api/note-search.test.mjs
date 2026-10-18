import test from 'node:test'
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { readFile, readdir } from 'node:fs/promises'
import { pathToFileURL } from 'node:url'

const enabled = Boolean(process.env.DATA_API_TEST_DATABASE_URL && process.env.DATA_API_RUNTIME_DIR)

test('search migration backfills existing notes and maintains a private RLS index atomically', { skip: !enabled, timeout: 60000 }, async t => {
  const require = createRequire(pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/package.json'))
  const { Client } = require('pg')
  const suffix = `${process.pid}_${Date.now()}`, database = `search_${suffix}`, role = `search_api_${suffix}`
  const admin = new Client({ connectionString: process.env.DATA_API_TEST_DATABASE_URL })
  await admin.connect()
  let owner, api
  try {
    await admin.query(`CREATE ROLE "${role}" LOGIN NOINHERIT NOBYPASSRLS PASSWORD 'test-only'`)
    await admin.query(`CREATE DATABASE "${database}"`)
    const source = new URL(process.env.DATA_API_TEST_DATABASE_URL); source.pathname = '/' + database
    owner = new Client({ connectionString: source.toString() }); await owner.connect()
    await owner.query(`REVOKE ALL ON SCHEMA public FROM PUBLIC; CREATE SCHEMA api; GRANT USAGE ON SCHEMA api TO "${role}";
      ALTER DEFAULT PRIVILEGES IN SCHEMA api GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO "${role}";
      ALTER DEFAULT PRIVILEGES IN SCHEMA api GRANT USAGE,SELECT ON SEQUENCES TO "${role}"`)
    const directory = new URL('../../cmd/gregale/templates/data-api-starter/migrations/sql/', import.meta.url)
    for (const file of (await readdir(directory)).filter(file => file.endsWith('.sql')).sort()) {
      if (file === '0016_note_search.sql') {
        await owner.query("INSERT INTO api.notes(subject,body) VALUES('alice','backfill running'),('alice','backfill running'),('bob','backfill running secret')")
      }
      await owner.query(await readFile(new URL(file, directory), 'utf8'))
    }
    assert.equal((await owner.query("SELECT count(*)::int AS n FROM api.note_search_terms WHERE subject='alice' AND term='run'")).rows[0].n, 2)
    assert.equal((await owner.query("SELECT has_function_privilege('public','api.search_notes(text,text,integer)','EXECUTE') AS allowed")).rows[0].allowed, false)
    await owner.query(`GRANT EXECUTE ON FUNCTION api.search_notes(text,text,integer) TO "${role}"`)
    source.username = role; source.password = 'test-only'
    api = new Client({ connectionString: source.toString() }); await api.connect()
    await api.query("SELECT set_config('request.jwt.claims','{\"sub\":\"alice\"}',false)")
    const first = (await api.query("SELECT * FROM api.search_notes('backfill run',NULL,1)")).rows[0]
    assert.deepEqual(first.results.map(row => row.id), [2]); assert.equal(first.has_more, true)
    const tail = (await api.query("SELECT * FROM api.search_notes('backfill run',$1,1)", [first.next_cursor])).rows[0]
    assert.deepEqual(tail.results.map(row => row.id), [1]); assert.equal(tail.next_cursor, null)
    assert.deepEqual((await api.query('SELECT * FROM api.note_search_terms')).rows, [])
    // A guessed capture marker still cannot bypass the trigger-depth guard.
    await api.query("SELECT set_config('gregale.note_search_scope','capture',false)")
    await assert.rejects(api.query("INSERT INTO api.note_search_terms VALUES('alice','forged',1)"), error => error.code === '42501')
    await api.query("SELECT set_config('gregale.note_search_scope','',false)")
    await api.query('BEGIN')
    await api.query("UPDATE api.notes SET body='backfill walking' WHERE id=2")
    assert.deepEqual((await api.query("SELECT * FROM api.search_notes('backfill run')")).rows[0].results.map(row => row.id), [1])
    await api.query('ROLLBACK')
    assert.equal((await api.query("SELECT * FROM api.search_notes('backfill run')")).rows[0].results.length, 2)
    // Owner identity moves rebuild both sides without leaving stale postings.
    await owner.query("UPDATE api.notes SET subject='bob' WHERE id=2")
    assert.equal((await owner.query("SELECT count(*)::int AS n FROM api.note_search_terms WHERE subject='alice' AND note_id=2")).rows[0].n, 0)
    assert.deepEqual((await api.query("SELECT * FROM api.search_notes('backfill run')")).rows[0].results.map(row => row.id), [1])
    await api.query('DELETE FROM api.notes WHERE id=1')
    assert.equal((await owner.query('SELECT count(*)::int AS n FROM api.note_search_terms WHERE note_id=1')).rows[0].n, 0)
    // Exercise large statements within the real serving query budget. A row
    // trigger with a cached full-scan plan regresses badly on this workload.
    await api.query('SET statement_timeout=15000')
    for (const [operation, sql, expected] of [
      ['insert', "INSERT INTO api.notes(subject,body) SELECT 'alice','bulk running' FROM generate_series(1,10002)", 20004],
      ['update', "UPDATE api.notes SET body='bulk walking' WHERE subject='alice'", 20004],
      ['delete', "DELETE FROM api.notes WHERE subject='alice'", 0],
    ]) {
      const started = performance.now(); await api.query(sql)
      t.diagnostic(`search index: 10002-row ${operation} ${Math.round(performance.now() - started)}ms`)
      assert.equal((await owner.query("SELECT count(*)::int AS n FROM api.note_search_terms WHERE subject='alice'")).rows[0].n, expected)
      if (operation === 'update') assert.equal((await owner.query("SELECT count(*)::int AS n FROM api.note_search_terms WHERE subject='alice' AND term='run'")).rows[0].n, 0)
    }
  } finally {
    await api?.end(); await owner?.end()
    await admin.query(`DROP DATABASE IF EXISTS "${database}" WITH (FORCE)`)
    await admin.query(`DROP ROLE IF EXISTS "${role}"`); await admin.end()
  }
})
