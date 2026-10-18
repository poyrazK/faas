import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'

const nodes = plan => [plan, ...(plan.Plans ?? []).flatMap(nodes)]

export async function verifyNoteSearch({ owner, client, root, loginURL, pg, userA, userB, url, makeClient }) {
  const { parseNoteSearchPage } = await import(pathToFileURL(join(root, 'client/dist/note-search.js')))
  const marker = 'gregalesearchprobe', query = `${marker} running`
  const ids = [], planPrefix = `search-plan-${randomUUID()}-`
  const direct = new pg.Client({ connectionString: loginURL }); await direct.connect()
  const search = async (text = query, options) => {
    const response = await client.search(text, options); assert.equal(response.error, null, JSON.stringify(response.error))
    assert.equal(response.data.length, 1); return parseNoteSearchPage(response.data[0], options?.after)
  }
  const failed = (response, message) => { assert.equal(response.status, 400, JSON.stringify(response.error)); assert.equal(response.error.message, message) }
  try {
    for (const body of [`${marker} running ${marker} running ${marker} running`, `${marker} running`, `${marker} running`, `${marker} with many extra filler words running`, `${marker} walked`, `${marker} running walking`]) {
      const result = await client.create(body); assert.equal(result.error, null); ids.push(result.data.id)
    }
    const foreign = await owner.query('INSERT INTO api.notes(subject,body) VALUES($1,$2) RETURNING id', [userB.subject, `${marker} running running running running`])
    ids.push(foreign.rows[0].id)
    const expected = (await owner.query(`SELECT id,
      round(ts_rank_cd(to_tsvector('pg_catalog.english',body),plainto_tsquery('pg_catalog.english',$2),32)::numeric*1000000)::integer AS rank
      FROM api.notes WHERE subject=$1 AND to_tsvector('pg_catalog.english',body) @@ plainto_tsquery('pg_catalog.english',$2)
      ORDER BY rank DESC,id DESC`, [userA.subject, query])).rows
    const first = await search(query, { size: 2 }); assert.equal(first.has_more, true)
    const found = [...first.results]; let page = first
    while (page.has_more) { page = await search(query, { size: 2, after: page.next_cursor }); found.push(...page.results) }
    assert.equal(page.next_cursor, null)
    assert.deepEqual(found.map(({ id, rank }) => ({ id, rank })), expected)
    assert.ok(found.every(row => !('subject' in row))); assert.ok(!found.some(row => row.id === foreign.rows[0].id))
    assert.ok(found[0].rank > found.at(-1).rank)
    const bob = makeClient({ url, subject: userB.subject, accessToken: userB.token })
    assert.deepEqual(parseNoteSearchPage((await bob.search(query)).data[0]).results.map(row => row.id), [foreign.rows[0].id])
    failed(await bob.db.rpc('search_notes', { search_text: query, after_cursor: first.next_cursor }).retry(false), 'note_search_cursor_invalid')
    failed(await client.db.rpc('search_notes', { search_text: query + ' ', after_cursor: first.next_cursor }).retry(false), 'note_search_cursor_invalid')
    assert.deepEqual((await search(`${marker} run`)).results.map(row => row.id), expected.map(row => row.id), 'English stemming')
    assert.deepEqual((await search(`${marker} running OR walking`)).results.map(row => row.id), [ids[5]], 'plain AND words, not Boolean syntax')
    assert.deepEqual((await search('unmatchedlexemexyz')).results, [])
    assert.ok((await search(query.padEnd(1024, ' '), { size: 100 })).results.length)
    assert.deepEqual((await search(Array.from({ length: 16 }, (_, i) => `lexeme${String.fromCharCode(97 + i)}`).join(' '))).results, [])

    for (const text of [null, '', 'the and', 'é'.repeat(513), Array.from({ length: 17 }, (_, i) => `lexeme${String.fromCharCode(97 + i)}`).join(' ')]) {
      failed(await client.db.rpc('search_notes', { search_text: text }).retry(false), 'note_search_query_invalid')
    }
    for (const size of [null, 0, -1, 101]) failed(await client.db.rpc('search_notes', { search_text: query, page_size: size }).retry(false), 'note_search_page_size_invalid')
    const prefix = first.next_cursor.slice(0, 68)
    for (const cursor of ['', 'x'.repeat(129), first.next_cursor + '\n', `${prefix}01.1`, `${prefix}1000001.1`, `${prefix}1.2147483648`, `${prefix}1.1.extra`]) {
      failed(await client.db.rpc('search_notes', { search_text: query, after_cursor: cursor }).retry(false), 'note_search_cursor_invalid')
    }

    // An additional policy on current notes remains authoritative over postings.
    await owner.query(`CREATE POLICY search_current_visibility ON api.notes AS RESTRICTIVE FOR SELECT USING(id<>${ids[0]})`)
    try { assert.ok(!(await search()).results.some(row => row.id === ids[0])) }
    finally { await owner.query('DROP POLICY search_current_visibility ON api.notes') }
    const edited = await client.update(ids[0], 1, { body: `${marker} walked` }); assert.equal(edited.error, null)
    assert.ok(!(await search()).results.some(row => row.id === ids[0]))
    assert.ok((await search(`${marker} walk`)).results.some(row => row.id === ids[0] && row.version === 2))
    assert.equal((await client.remove(ids[1])).error, null)
    assert.ok(!(await search()).results.some(row => row.id === ids[1]))
    assert.equal((await owner.query('SELECT count(*)::integer AS n FROM api.note_search_terms WHERE note_id=$1', [ids[1]])).rows[0].n, 0)

    const hidden = await client.db.from('note_search_terms').select().retry(false); assert.equal(hidden.error, null); assert.deepEqual(hidden.data, [])
    const forged = await client.db.from('note_search_terms').insert({ subject: userA.subject, term: 'forged', note_id: ids[0] }).retry(false)
    assert.equal(forged.error.code, '42501')
    await assert.rejects(owner.query('DELETE FROM api.note_search_terms WHERE note_id=$1', [ids[0]]), error => error.code === '42501')
    await direct.query("SELECT set_config('request.jwt.claims',$1,false)", [JSON.stringify({ sub: userA.subject })])
    await direct.query('BEGIN READ ONLY')
    await direct.query("SELECT set_config('gregale.note_search_scope','before-search',true)")
    assert.ok((await direct.query('SELECT * FROM api.search_notes($1)', [query])).rows[0].results.length)
    assert.equal((await direct.query("SELECT current_setting('gregale.note_search_scope',true) AS scope")).rows[0].scope, 'before-search')
    await direct.query('ROLLBACK')
    await direct.query('BEGIN')
    await direct.query('UPDATE api.notes SET body=$1 WHERE id=$2', [`${marker} rollbackword`, ids[2]])
    assert.ok(!(await direct.query('SELECT * FROM api.search_notes($1)', [query])).rows[0].results.some(row => row.id === ids[2]))
    await direct.query('ROLLBACK')
    assert.ok((await search()).results.some(row => row.id === ids[2]), 'rollback restores both note and index')
    await direct.query("SELECT set_config('request.jwt.claims','{}',false)")
    await assert.rejects(direct.query('SELECT * FROM api.search_notes($1)', [query]), error => error.code === '42501')

    // Verify the candidate lookup with actual active RLS and planner defaults.
    await owner.query(`INSERT INTO api.notes(subject,body)
      SELECT $1||(n%40)::text,CASE WHEN n=40 THEN 'searchindexneedle' ELSE 'searchindexfiller' END FROM generate_series(1,6000) n`, [planPrefix])
    await owner.query('ANALYZE api.note_search_terms; ANALYZE api.notes')
    await direct.query('BEGIN READ ONLY')
    await direct.query("SELECT set_config('request.jwt.claims',$1,true),set_config('gregale.note_search_scope','read',true)", [JSON.stringify({ sub: planPrefix + '0' })])
    assert.deepEqual((await direct.query("SELECT rolsuper,rolbypassrls,row_security_active('api.note_search_terms') AS active FROM pg_roles WHERE rolname=current_user")).rows[0], { rolsuper: false, rolbypassrls: false, active: true })
    const terms = (await direct.query("SELECT tsvector_to_array(to_tsvector('pg_catalog.english','searchindexneedle')) AS terms")).rows[0].terms
    const sql = `SELECT p.note_id FROM unnest($2::text[]) w(term) JOIN api.note_search_terms p ON p.subject=$1 AND p.term=w.term GROUP BY p.note_id HAVING count(*)=cardinality($2::text[])`
    const plan = (await direct.query('EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) ' + sql, [planPrefix + '0', terms])).rows[0]['QUERY PLAN'][0].Plan
    const planned = nodes(plan)
    assert.ok(planned.some(node => node['Index Name'] === 'note_search_terms_pkey'), 'serving_role_search_term_index_unused')
    assert.ok(!planned.some(node => node['Relation Name'] === 'note_search_terms' && node['Node Type'] === 'Seq Scan'), 'search_scans_all_postings')
    assert.equal((await direct.query(sql, [planPrefix + '0', terms])).rows.length, 1)
    await direct.query('ROLLBACK')
  } finally {
    await direct.query('ROLLBACK'); await direct.end()
    await owner.query('DELETE FROM api.notes WHERE id=ANY($1::int[]) OR subject LIKE $2', [ids, planPrefix + '%'])
    assert.equal((await owner.query('SELECT count(*)::integer AS n FROM api.note_search_terms WHERE note_id=ANY($1::int[]) OR subject LIKE $2', [ids, planPrefix + '%'])).rows[0].n, 0)
  }
}
