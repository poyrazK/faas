import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import pg from 'pg'

// Change hooks SQL contract (ADR-20261022121642) on a real PostgreSQL. Set
// DATA_API_CHANGE_HOOKS_PG_URL to a superuser URL of a disposable server; the
// test creates and drops its own database and roles, mirroring a managed
// data_api binding: a NOLOGIN schema owner and a NOINHERIT serving login with
// default DML on the api schema only.

const admin = process.env.DATA_API_CHANGE_HOOKS_PG_URL
const suffix = `${process.pid}_${Date.now()}`
const db = `change_hooks_${suffix}`, owner = `hooks_owner_${suffix}`, login = `hooks_login_${suffix}`
const userClaims = JSON.stringify({ sub: 'alice', gregale_access: 'user' })
const relayClaims = JSON.stringify({ gregale_access: 'hooks' })

async function connect(url, database, user) {
  const target = new URL(url)
  target.pathname = `/${database}`
  if (user) { target.username = user; target.password = 'hooks-test' }
  const client = new pg.Client({ connectionString: target.href })
  await client.connect()
  return client
}

// as runs statements in one transaction with request.jwt.claims set, as
// PostgREST does for each request.
async function as(client, claims, work) {
  await client.query('BEGIN')
  try {
    await client.query("SELECT set_config('request.jwt.claims', $1, true)", [claims])
    const result = await work()
    await client.query('COMMIT')
    return result
  } catch (error) { await client.query('ROLLBACK'); throw error }
}

test('change hooks record committed changes and only the relay reads them', { skip: !admin && 'set DATA_API_CHANGE_HOOKS_PG_URL' }, async t => {
  const root = await connect(admin, 'postgres')
  await root.query(`CREATE DATABASE ${db}`)
  const clients = []
  t.after(async () => {
    for (const client of clients) await client.end().catch(() => {})
    await root.query(`DROP DATABASE IF EXISTS ${db} WITH (FORCE)`)
    await root.query(`DROP ROLE IF EXISTS ${login}`)
    await root.query(`DROP ROLE IF EXISTS ${owner}`)
    await root.end()
  })
  const su = await connect(admin, db)
  clients.push(su)
  for (const statement of [
    `CREATE ROLE ${owner} NOLOGIN NOINHERIT`,
    `GRANT CONNECT, CREATE ON DATABASE ${db} TO ${owner}`,
    `REVOKE CREATE, TEMPORARY ON DATABASE ${db} FROM PUBLIC`,
    `ALTER SCHEMA public OWNER TO ${owner}`,
    'REVOKE CREATE ON SCHEMA public FROM PUBLIC',
    `ALTER DEFAULT PRIVILEGES FOR ROLE ${owner} REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC`,
    `CREATE SCHEMA api AUTHORIZATION ${owner}`,
    'REVOKE ALL ON SCHEMA api FROM PUBLIC',
    `CREATE ROLE ${login} LOGIN NOINHERIT PASSWORD 'hooks-test'`,
    `GRANT CONNECT ON DATABASE ${db} TO ${login}`,
    `GRANT USAGE ON SCHEMA api TO ${login}`,
    `ALTER DEFAULT PRIVILEGES FOR ROLE ${owner} IN SCHEMA api GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO ${login}`,
    `ALTER DEFAULT PRIVILEGES FOR ROLE ${owner} IN SCHEMA api GRANT USAGE, SELECT ON SEQUENCES TO ${login}`,
  ]) await su.query(statement)

  // The owner's migration: the printed SQL, then a hooked table.
  const sql = (await readFile(new URL('../change-hooks.sql', import.meta.url), 'utf8')).replaceAll('{{schema}}', 'api').replaceAll('{{backlog_max}}', '100000')
  await su.query(`SET ROLE ${owner}`)
  await su.query(sql)
  await su.query(`CREATE TABLE api.orders (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, status text NOT NULL, secret text, note text);
    ALTER TABLE api.orders ENABLE ROW LEVEL SECURITY;
    CREATE POLICY open_orders ON api.orders USING (true) WITH CHECK (true);
    CREATE TABLE api.plain (name text);
    CREATE TABLE api.audit (id int PRIMARY KEY, body text);
    ALTER TABLE api.audit ENABLE ROW LEVEL SECURITY;
    CREATE POLICY open_audit ON api.audit USING (true) WITH CHECK (true);`)
  await su.query("SELECT gregale_hooks.enable('api.orders', exclude => ARRAY['secret'])")
  await su.query("SELECT gregale_hooks.enable('api.audit', payload => 'keys')")

  await t.test('enable refuses tables it cannot hook', async () => {
    for (const [statement, code] of [
      ["SELECT gregale_hooks.enable('api.plain')", '55000'],
      ["SELECT gregale_hooks.enable('api.gregale_change_events')", '22023'],
      ["SELECT gregale_hooks.enable('api.orders', exclude => ARRAY['id'])", '22023'],
      ["SELECT gregale_hooks.enable('api.orders', exclude => ARRAY['nope'])", '42703'],
      ["SELECT gregale_hooks.enable('api.orders', payload => 'all')", '22023'],
    ]) {
      await su.query('BEGIN')
      await assert.rejects(su.query(statement), error => error.code === code, statement)
      await su.query('ROLLBACK')
    }
  })
  await su.query('RESET ROLE')

  const serving = await connect(admin, db, login)
  clients.push(serving)

  await t.test('the serving login stays within the data_api contract', async () => {
    // The same checks a managed binding's credential verification runs.
    const { rows } = await su.query(`SELECT
      has_database_privilege($1,current_database(),'CREATE,TEMPORARY') AS database_create,
      EXISTS(SELECT 1 FROM pg_namespace n WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema' AND has_schema_privilege($1,n.oid,'CREATE')) AS schema_create,
      EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname NOT IN ('api','information_schema') AND n.nspname !~ '^pg_' AND c.relkind IN ('r','p','v','m','f') AND has_table_privilege($1,c.oid,'SELECT,INSERT,UPDATE,DELETE')) AS outside_tables,
      EXISTS(SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname IN ('api','public') AND p.prosecdef AND has_function_privilege($1,p.oid,'EXECUTE')) AS definer_execution,
      has_function_privilege($1,'gregale_hooks.capture()','EXECUTE') AS capture_execution,
      has_table_privilege($1,'api.gregale_change_events','SELECT,UPDATE,DELETE') AS relay_tables,
      EXISTS(SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='api' AND obj_description(p.oid,'pg_proc')='@gregale:rpc') AS exposed_rpc`, [login])
    assert.deepEqual(rows[0], { database_create: false, schema_create: false, outside_tables: false, definer_execution: false, capture_execution: false, relay_tables: true, exposed_rpc: false })
  })

  const events = async () => (await su.query('SELECT relation_schema, relation_name, op, row_key, old_row, new_row, changed FROM api.gregale_change_events ORDER BY id')).rows

  await t.test('serving writes record events without seeing them', async () => {
    await as(serving, userClaims, () => serving.query("INSERT INTO api.orders (status, secret, note) VALUES ('new', 'hunter2', 'first')"))
    await as(serving, userClaims, () => serving.query("UPDATE api.orders SET note = 'first' WHERE id = 1"))
    await as(serving, userClaims, () => serving.query("UPDATE api.orders SET secret = 'rotated' WHERE id = 1"))
    await as(serving, userClaims, () => serving.query("UPDATE api.orders SET status = 'paid' WHERE id = 1"))
    await assert.rejects(as(serving, userClaims, async () => {
      await serving.query("INSERT INTO api.orders (status) VALUES ('rolled back')")
      throw new Error('abort')
    }), /abort/)
    await as(serving, userClaims, () => serving.query("INSERT INTO api.audit VALUES (7, 'body')"))
    await as(serving, userClaims, async () => {
      await serving.query("SET LOCAL gregale_hooks.skip = 'on'")
      await serving.query("INSERT INTO api.orders (status) VALUES ('skipped')")
    })
    await as(serving, userClaims, () => serving.query('DELETE FROM api.orders WHERE id = 1'))
    assert.deepEqual(await events(), [
      { relation_schema: 'api', relation_name: 'orders', op: 'insert', row_key: { id: 1 }, old_row: null, new_row: { id: 1, note: 'first', status: 'new' }, changed: null },
      { relation_schema: 'api', relation_name: 'orders', op: 'update', row_key: { id: 1 }, old_row: { id: 1, note: 'first', status: 'new' }, new_row: { id: 1, note: 'first', status: 'paid' }, changed: ['status'] },
      { relation_schema: 'api', relation_name: 'audit', op: 'insert', row_key: { id: 7 }, old_row: null, new_row: null, changed: null },
      { relation_schema: 'api', relation_name: 'orders', op: 'delete', row_key: { id: 1 }, old_row: { id: 1, note: 'first', status: 'paid' }, new_row: null, changed: null },
    ], 'no-op and excluded-only updates, rollbacks and skipped writes record nothing; excluded columns never appear')
    assert.equal((await as(serving, relayClaims, () => serving.query('SELECT count(*)::int AS n FROM api.gregale_change_drops'))).rows[0].n, 1, 'the relay reads the drop note')
    for (const claims of [userClaims, JSON.stringify({ gregale_access: 'server' }), JSON.stringify({ gregale_access: 'anonymous' })]) {
      assert.equal((await as(serving, claims, () => serving.query('SELECT count(*)::int AS n FROM api.gregale_change_events'))).rows[0].n, 0, claims)
      assert.equal((await as(serving, claims, () => serving.query('SELECT count(*)::int AS n FROM api.gregale_change_drops'))).rows[0].n, 0, claims)
      await assert.rejects(as(serving, claims, () => serving.query('UPDATE api.gregale_change_drops SET last_dropped_at = NULL RETURNING 1').then(r => { if (!r.rowCount) throw Object.assign(new Error('no rows'), { code: '42501' }) })), error => error.code === '42501')
      await assert.rejects(as(serving, claims, () => serving.query("SELECT * FROM api.gregale_change_claim('r1', 10, 60000)")), error => error.code === '42501')
      await assert.rejects(as(serving, claims, () => serving.query("INSERT INTO api.gregale_change_events (relation_schema, relation_name, op, row_key) VALUES ('api','orders','insert','{}')")), error => error.code === '42501')
    }
  })

  const claim = (relay, max = 100, lease = 60000) => as(serving, relayClaims, async () => (await serving.query('SELECT id, attempts FROM api.gregale_change_claim($1, $2, $3) ORDER BY id', [relay, max, lease])).rows)
  const settle = (relay, published, failed = [], retry = 0) => as(serving, relayClaims, () => serving.query('SELECT api.gregale_change_settle($1, $2, $3, $4)', [relay, published, JSON.stringify(failed), retry]))

  await t.test('one relay claims at a time, in order, and settles its own claim', async () => {
    const first = await claim('relay-a', 2)
    assert.deepEqual(first.map(r => r.attempts), [1, 1])
    assert.deepEqual(await claim('relay-b'), [], 'another relay waits while a lease is held')
    // relay-b cannot settle relay-a's events.
    await settle('relay-b', first.map(r => r.id))
    assert.equal((await events()).length, 4)
    await settle('relay-a', [first[0].id], [{ id: first[1].id, code: 'event_schema_invalid' }])
    const failed = (await su.query('SELECT failure_code FROM api.gregale_change_events WHERE failed_at IS NOT NULL')).rows
    assert.deepEqual(failed, [{ failure_code: 'event_schema_invalid' }])
    const rest = await claim('relay-b')
    assert.equal(rest.length, 2, 'a failed event does not block later ones')
    // A retryable failure holds back every later event until it is due.
    await settle('relay-b', [], [], 3600000)
    assert.deepEqual(await claim('relay-b'), [])
    await su.query("UPDATE api.gregale_change_events SET available_at = '-infinity' WHERE id = $1", [rest[1].id])
    assert.deepEqual(await claim('relay-b'), [], 'the earlier event still waits')
    await su.query("UPDATE api.gregale_change_events SET available_at = '-infinity'")
    const again = await claim('relay-b')
    assert.deepEqual(again.map(r => r.attempts), [2, 2])
    await settle('relay-b', again.map(r => r.id))
    assert.deepEqual((await events()).length, 1)
  })

  await t.test('an expired lease passes to another relay', async () => {
    await as(serving, userClaims, () => serving.query("INSERT INTO api.orders (status) VALUES ('later')"))
    assert.equal((await claim('relay-a', 100, 1000)).length, 1)
    await new Promise(resolve => setTimeout(resolve, 1100))
    const taken = await claim('relay-b')
    assert.equal(taken.length, 1)
    await settle('relay-a', taken.map(r => r.id))
    assert.equal((await events()).length, 2, 'the expired relay cannot settle a claim it lost')
    await settle('relay-b', taken.map(r => r.id))
  })

  await t.test('the owner retries or discards failed events', async () => {
    await su.query(`SET ROLE ${owner}`)
    const before = (await su.query('SELECT id, event_key, captured_at FROM api.gregale_change_events')).rows[0]
    assert.equal((await su.query('SELECT gregale_hooks.retry_failed() AS n')).rows[0].n, '1')
    const after = (await su.query('SELECT id, event_key, captured_at, failed_at FROM api.gregale_change_events')).rows[0]
    assert.ok(Number(after.id) > Number(before.id), 'a retried event takes a new identifier, after newer events')
    assert.deepEqual([after.event_key, after.captured_at, after.failed_at], [before.event_key, before.captured_at, null], 'it keeps its event id and time')
    assert.equal((await claim('relay-c')).length, 1)
    await settle('relay-c', [], [{ id: (await su.query('SELECT id FROM api.gregale_change_events')).rows[0].id, code: 'rejected' }])
    assert.equal((await su.query('SELECT gregale_hooks.discard_failed() AS n')).rows[0].n, '1')
    await su.query("SELECT gregale_hooks.disable('api.orders')")
    await su.query('RESET ROLE')
    await as(serving, userClaims, () => serving.query("INSERT INTO api.orders (status) VALUES ('unhooked')"))
    assert.deepEqual(await events(), [])
  })

  await t.test('a partitioned table records changes under its own name', async () => {
    await su.query(`SET ROLE ${owner}`)
    await su.query(`CREATE TABLE api.metrics (day date, id int, value int, PRIMARY KEY (day, id)) PARTITION BY RANGE (day);
      CREATE TABLE api.metrics_2026 PARTITION OF api.metrics FOR VALUES FROM ('2026-01-01') TO ('2027-01-01');
      ALTER TABLE api.metrics ENABLE ROW LEVEL SECURITY;
      CREATE POLICY open_metrics ON api.metrics USING (true) WITH CHECK (true);`)
    await su.query("SELECT gregale_hooks.enable('api.metrics')")
    await su.query("SELECT gregale_hooks.set_schema_version('api.metrics', 'v1')")
    await su.query('RESET ROLE')
    await as(serving, userClaims, () => serving.query("INSERT INTO api.metrics VALUES ('2026-10-22', 1, 5)"))
    assert.deepEqual((await events()).map(e => [e.relation_name, e.row_key]), [['metrics', { day: '2026-10-22', id: 1 }]])
    assert.equal((await su.query("SELECT schema_version FROM api.gregale_change_events WHERE relation_name='metrics'")).rows[0].schema_version, 'v1', 'partition changes use the hooked parent version')
    await su.query('DELETE FROM api.gregale_change_events')
  })

  await t.test('rows over 64 KiB are left out of the event', async () => {
    await su.query(`SET ROLE ${owner}`)
    await su.query(`CREATE TABLE api.docs (id int PRIMARY KEY, body text);
      ALTER TABLE api.docs ENABLE ROW LEVEL SECURITY;
      CREATE POLICY open_docs ON api.docs USING (true) WITH CHECK (true);`)
    await su.query("SELECT gregale_hooks.enable('api.docs')")
    await su.query('RESET ROLE')
    await as(serving, userClaims, () => serving.query("INSERT INTO api.docs VALUES (1, repeat('x', 70000)), (2, 'small')"))
    const { rows } = await su.query('SELECT row_key, new_row, truncated FROM api.gregale_change_events ORDER BY id')
    assert.deepEqual(rows, [{ row_key: { id: 1 }, new_row: null, truncated: true }, { row_key: { id: 2 }, new_row: { id: 2, body: 'small' }, truncated: false }])
    await su.query('DELETE FROM api.gregale_change_events')
  })

  await t.test('claims and settlements reject malformed arguments', async () => {
    for (const statement of ["SELECT * FROM api.gregale_change_claim('bad relay', 10, 60000)", "SELECT * FROM api.gregale_change_claim('r', 101, 60000)", "SELECT * FROM api.gregale_change_claim('r', 10, 10)", "SELECT api.gregale_change_settle('r', '{}', '{}', 0)"]) {
      await assert.rejects(as(serving, relayClaims, () => serving.query(statement)), error => error.code === '22023', statement)
    }
  })

  await t.test('schema versions are captured at write time and upgrade is repeatable', async () => {
    await su.query(`SET ROLE ${owner}`)
    await su.query(sql)
    await su.query(`CREATE TABLE api.versioned (id integer PRIMARY KEY, body text)`)
    await su.query("SELECT gregale_hooks.enable('api.versioned')")
    await su.query("SELECT gregale_hooks.set_schema_version('api.versioned', 'v1')")
    await su.query("INSERT INTO api.versioned VALUES(1, 'one')")
    await su.query("SELECT gregale_hooks.set_schema_version('api.versioned', 'v2')")
    await su.query("UPDATE api.versioned SET body='two' WHERE id=1")
    await su.query("SELECT gregale_hooks.set_schema_version('api.versioned', NULL)")
    await su.query("UPDATE api.versioned SET body='three' WHERE id=1")
    const versions = await su.query("SELECT event_key, schema_version FROM api.gregale_change_events WHERE relation_name='versioned' ORDER BY id")
    assert.deepEqual(versions.rows.map(row => row.schema_version), ['v1', 'v2', null])
    await su.query("UPDATE api.gregale_change_events SET failed_at=clock_timestamp() WHERE event_key=$1", [versions.rows[0].event_key])
    await su.query('SELECT gregale_hooks.retry_failed()')
    const retried = await su.query('SELECT schema_version FROM api.gregale_change_events WHERE event_key=$1', [versions.rows[0].event_key])
    assert.equal(retried.rows[0].schema_version, 'v1', 'owner retry must preserve the captured version after the selection changes')
    await su.query('RESET ROLE')
    await assert.rejects(as(serving, userClaims, () => serving.query("SELECT gregale_hooks.set_schema_version('api.versioned', 'v3')")), error => error.code === '42501')
  })

})

// Amendment 2: while the bound's worth of changes waits, capture records no
// more, so a backlog the platform never takes cannot fill the database.
test('capture stops recording at the backlog bound and resumes as changes publish', { skip: !admin && 'set DATA_API_CHANGE_HOOKS_PG_URL' }, async t => {
  const bounded = `${db}_bound`
  const root = await connect(admin, 'postgres')
  await root.query(`CREATE DATABASE ${bounded}`)
  const su = await connect(admin, bounded)
  t.after(async () => {
    await su.end().catch(() => {})
    await root.query(`DROP DATABASE IF EXISTS ${bounded} WITH (FORCE)`)
    await root.end()
  })
  const sql = (await readFile(new URL('../change-hooks.sql', import.meta.url), 'utf8')).replaceAll('{{schema}}', 'api').replaceAll('{{backlog_max}}', '3')
  await su.query('CREATE SCHEMA api')
  await su.query(sql)
  await su.query('CREATE TABLE api.orders (id int PRIMARY KEY, status text NOT NULL)')
  await su.query("SELECT gregale_hooks.enable('api.orders')")
  const recorded = async () => (await su.query('SELECT relation_name, row_key FROM api.gregale_change_events ORDER BY id')).rows.map(r => r.row_key.id)
  for (let id = 1; id <= 5; id++) await su.query('INSERT INTO api.orders VALUES ($1, $2)', [id, 'new'])
  assert.deepEqual(await recorded(), [1, 2, 3], 'changes past the bound are not recorded')
  const { rows: [check] } = await su.query(`SELECT EXISTS (SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
    WHERE n.nspname = 'gregale_hooks' AND p.proname = 'capture' AND p.prosrc LIKE '%gregale:backlog-bound%') AS bounded`)
  assert.equal(check.bounded, true, 'the runtime can see the bound is installed')
  // The relay published the oldest change; recording resumes.
  await su.query('DELETE FROM api.gregale_change_events WHERE id = (SELECT min(id) FROM api.gregale_change_events)')
  await su.query('INSERT INTO api.orders VALUES (6, $1)', ['new'])
  assert.deepEqual(await recorded(), [2, 3, 6])
  // Rejected changes do not hold the bound: only waiting ones count.
  await su.query('UPDATE api.gregale_change_events SET failed_at = now(), failure_code = $1', ['rejected'])
  await su.query('INSERT INTO api.orders VALUES (7, $1)', ['new'])
  assert.deepEqual(await recorded(), [2, 3, 6, 7])
})

// Amendment 3: a change left out at the bound is noted for the relay to
// report; discard_waiting drops what waits and clears the note; a retried
// change takes a new identifier, so an old one does not trip the bound.
test('changes left out at the bound are noted, and the owner can drop what waits', { skip: !admin && 'set DATA_API_CHANGE_HOOKS_PG_URL' }, async t => {
  const noted = `${db}_drops`
  const root = await connect(admin, 'postgres')
  await root.query(`CREATE DATABASE ${noted}`)
  const su = await connect(admin, noted)
  t.after(async () => {
    await su.end().catch(() => {})
    await root.query(`DROP DATABASE IF EXISTS ${noted} WITH (FORCE)`)
    await root.end()
  })
  const sql = (await readFile(new URL('../change-hooks.sql', import.meta.url), 'utf8')).replaceAll('{{schema}}', 'api').replaceAll('{{backlog_max}}', '3')
  await su.query('CREATE SCHEMA api')
  await su.query(sql)
  await su.query(sql.slice(sql.indexOf('-- gregale:upgrade-begin'), sql.indexOf('-- gregale:upgrade-end')))
  await su.query('CREATE TABLE api.orders (id int PRIMARY KEY, status text NOT NULL)')
  await su.query("SELECT gregale_hooks.enable('api.orders')")
  const note = async () => (await su.query('SELECT last_dropped_at FROM api.gregale_change_drops')).rows
  assert.deepEqual(await note(), [{ last_dropped_at: null }], 'the upgrade section replays without duplicating the note')
  for (let id = 1; id <= 3; id++) await su.query('INSERT INTO api.orders VALUES ($1, $2)', [id, 'new'])
  assert.equal((await note())[0].last_dropped_at, null, 'nothing left out yet')
  await su.query('INSERT INTO api.orders VALUES (4, $1)', ['new'])
  const dropped = (await note())[0].last_dropped_at
  assert.ok(dropped instanceof Date, 'the left-out change is noted')
  assert.equal((await su.query('SELECT gregale_hooks.discard_waiting() AS n')).rows[0].n, '3')
  assert.deepEqual(await note(), [{ last_dropped_at: null }])
  await su.query('INSERT INTO api.orders VALUES (5, $1)', ['new'])
  assert.equal((await su.query('SELECT count(*)::int AS n FROM api.gregale_change_events')).rows[0].n, 1, 'recording resumes')
  // A change rejected long ago, retried after many later changes published.
  await su.query("UPDATE api.gregale_change_events SET failed_at = now(), failure_code = 'rejected'")
  for (let id = 6; id <= 12; id++) {
    await su.query('INSERT INTO api.orders VALUES ($1, $2)', [id, 'new'])
    await su.query('DELETE FROM api.gregale_change_events WHERE failed_at IS NULL')
  }
  assert.equal((await su.query('SELECT gregale_hooks.retry_failed() AS n')).rows[0].n, '1')
  await su.query('INSERT INTO api.orders VALUES (13, $1)', ['new'])
  assert.equal((await su.query('SELECT count(*)::int AS n FROM api.gregale_change_events')).rows[0].n, 2, 'the retried change does not trip the bound')
})
