import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'

function nodes(plan) { return [plan, ...(plan.Plans ?? []).flatMap(nodes)] }

export async function verifyQueryPlans({ owner, loginURL, pg }) {
  const prefix = `plan-${randomUUID()}-`
  const subject = prefix + '0'
  const client = new pg.Client({ connectionString: loginURL })
  await client.connect()
  try {
    // 30,000 notes spread over 200 owners, plus comments and both link paths.
    await owner.query(`INSERT INTO api.notes (subject, body, priority, created_at)
      SELECT $1 || (n % 200)::text, 'query-plan', n % 3, now() - n * interval '1 second'
      FROM generate_series(1,30000) AS n`, [prefix])
    await owner.query(`INSERT INTO api.tags (subject, name)
      SELECT DISTINCT subject, 'plan tag' FROM api.notes WHERE subject LIKE $1`, [prefix + '%'])
    await owner.query(`INSERT INTO api.comments (subject, note_id, body)
      SELECT subject, id, 'plan reply' FROM api.notes WHERE subject LIKE $1`, [prefix + '%'])
    for (const table of ['note_tags', 'note_favorite_tags']) {
      await owner.query(`INSERT INTO api.${table} (subject, note_id, tag_id)
        SELECT n.subject, n.id, t.id FROM api.notes n JOIN api.tags t USING (subject) WHERE n.subject LIKE $1`, [prefix + '%'])
    }
    for (const table of ['notes', 'comments', 'tags', 'note_tags', 'note_favorite_tags']) await owner.query(`ANALYZE api.${table}`)
    await client.query('BEGIN')
    await client.query("SELECT set_config('request.jwt.claims',$1,true)", [JSON.stringify({ sub: subject })])
    const authority = (await client.query("SELECT rolbypassrls, rolsuper, row_security_active('api.notes'::regclass) AS active FROM pg_roles WHERE rolname=current_user")).rows[0]
    assert.deepEqual(authority, { rolbypassrls: false, rolsuper: false, active: true })
    assert.equal((await client.query('SELECT count(*)::integer AS count FROM api.notes')).rows[0].count, 150, 'plan_fixture_rls_leak')
    const rows = (await client.query('SELECT id, created_at FROM api.notes ORDER BY created_at DESC,id DESC OFFSET 50 LIMIT 1')).rows
    // Read timestamps as SQL text to preserve precision in the cursor query.
    const cursor = (await client.query('SELECT id, created_at::text AS stamp FROM api.notes WHERE id=$1', [rows[0].id])).rows[0]
    async function check(sql, params, index, ordered = false) {
      const result = await client.query('EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) ' + sql, params)
      const planNodes = nodes(result.rows[0]['QUERY PLAN'][0].Plan)
      assert.ok(planNodes.some(node => node['Index Name'] === index), `query_plan_missing_${index}`)
      if (ordered === true) assert.ok(!planNodes.some(node => ['Sort', 'Incremental Sort'].includes(node['Node Type'])), 'pagination_requires_sort')
      if (ordered === 'bounded') {
        for (const node of planNodes.filter(node => ['Sort', 'Incremental Sort'].includes(node['Node Type']))) {
          assert.ok(node.Plans.every(input => input['Actual Rows'] <= 150), 'cursor_sort_exceeds_owner_rows')
        }
      }
      assert.ok(!planNodes.some(node => node['Node Type'] === 'Seq Scan' && ['notes', 'comments', 'note_tags', 'note_favorite_tags'].includes(node['Relation Name'])), 'query_plan_scans_full_relation')
    }
    await check('SELECT id,body,created_at FROM api.notes ORDER BY created_at DESC,id DESC LIMIT 20', [], 'notes_owner_cursor_idx', true)
    await check('SELECT id,body,created_at FROM api.notes ORDER BY created_at DESC,id DESC OFFSET 50 LIMIT 20', [], 'notes_owner_cursor_idx', true)
    await check('SELECT id,body,created_at FROM api.notes WHERE priority=1 ORDER BY created_at DESC,id DESC LIMIT 20', [], 'notes_owner_priority_cursor_idx', true)
    await check('SELECT id,body,created_at FROM api.notes WHERE created_at < $1 OR (created_at=$1 AND id<$2) ORDER BY created_at DESC,id DESC LIMIT 20', [cursor.stamp, cursor.id], 'notes_owner_cursor_idx', true)
    await check('SELECT id,body,created_at FROM api.notes WHERE priority=1 AND (created_at < $1 OR (created_at=$1 AND id<$2)) ORDER BY created_at DESC,id DESC LIMIT 20', [cursor.stamp, cursor.id], 'notes_owner_priority_cursor_idx', 'bounded')
    await check('SELECT id,body FROM api.comments WHERE note_id=$1', [cursor.id], 'comments_owner_note_idx')
    const tag = (await client.query('SELECT id FROM api.tags LIMIT 1')).rows[0].id
    for (const table of ['note_tags', 'note_favorite_tags']) {
      await check(`SELECT n.id,n.body FROM api.${table} j JOIN api.notes n ON n.subject=j.subject AND n.id=j.note_id WHERE j.tag_id=$1`, [tag], `${table}_owner_tag_idx`)
      await check(`SELECT t.id,t.name FROM api.${table} j JOIN api.tags t ON t.subject=j.subject AND t.id=j.tag_id WHERE j.note_id=$1`, [cursor.id], `${table}_pkey`)
    }
  } finally {
    await client.query('ROLLBACK').catch(() => {})
    await client.end()
    await owner.query('DELETE FROM api.notes WHERE subject LIKE $1', [prefix + '%'])
    await owner.query('DELETE FROM api.tags WHERE subject LIKE $1', [prefix + '%'])
  }
}
