import assert from 'node:assert/strict'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'

export async function verifyPermissions({ root, owner, admin, role, migrationURL, loginURL, scope, database }) {
  const { permissions } = await import(pathToFileURL(join(root, 'migrations/rpc-permissions.mjs')))
  const { release } = await import(pathToFileURL(join(root, 'migrations/release.mjs')))
  const options = { apply: true }
  const preview = await permissions(migrationURL)
  assert.equal(preview.role, role)
  assert.deepEqual(preview.missing, ['create_note_with_tags', 'create_note_with_tags_once'])
  assert.equal(preview.ready, false)
  await assert.rejects(permissions(loginURL, options), /API schema owner required/)
  const applied = await release(migrationURL)
  assert.equal(applied.ready, true)
  assert.equal(applied.refreshRequired, true)
  assert.equal((await permissions(migrationURL)).ready, true)
  assert.equal((await permissions(migrationURL, options)).refreshRequired, false)
  await owner.query(`CREATE FUNCTION api.permission_private(value text) RETURNS text LANGUAGE sql AS 'SELECT value';
    REVOKE ALL ON FUNCTION api.permission_private(text) FROM PUBLIC;
    CREATE FUNCTION api.permission_unsafe(value text) RETURNS text LANGUAGE sql SECURITY DEFINER AS 'SELECT value';
    REVOKE ALL ON FUNCTION api.permission_unsafe(text) FROM PUBLIC;
    COMMENT ON FUNCTION api.permission_unsafe(text) IS '@gregale:rpc'`)
  try {
    const unsafe = await permissions(migrationURL)
    assert.ok(unsafe.blockers.includes('unsupported_or_unowned_function:permission_unsafe'))
    await assert.rejects(release(migrationURL), /Permission setup blocked/)
    assert.equal((await owner.query("SELECT has_function_privilege($1,'api.permission_unsafe(text)','EXECUTE') AS allowed", [role])).rows[0].allowed, false)
    assert.equal((await owner.query("SELECT has_function_privilege($1,'api.permission_private(text)','EXECUTE') AS allowed", [role])).rows[0].allowed, false)
  } finally { await owner.query('DROP FUNCTION api.permission_private(text), api.permission_unsafe(text)') }
  await owner.query('ALTER TABLE api.notes DISABLE ROW LEVEL SECURITY')
  try { assert.ok((await permissions(migrationURL)).blockers.includes('relation_not_ready:notes')); await assert.rejects(release(migrationURL), /Permission setup blocked/) }
  finally { await owner.query('ALTER TABLE api.notes ENABLE ROW LEVEL SECURITY') }
  await owner.query(`REVOKE USAGE ON SCHEMA api FROM "${role}"`)
  try { assert.ok((await permissions(migrationURL)).blockers.includes('schema_unsafe')) }
  finally { await owner.query(`GRANT USAGE ON SCHEMA api TO "${role}"`) }
  await owner.query(`ALTER DEFAULT PRIVILEGES IN SCHEMA api REVOKE SELECT ON TABLES FROM "${role}"`)
  try { assert.ok((await permissions(migrationURL)).blockers.includes('future_table_grants_missing')); await assert.rejects(release(migrationURL), /Permission setup blocked/) }
  finally { await owner.query(`ALTER DEFAULT PRIVILEGES IN SCHEMA api GRANT SELECT ON TABLES TO "${role}"`) }
  const rotated = `${role}_rotated`
  await admin.query(`CREATE ROLE "${rotated}" LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD 'test-only';
    COMMENT ON ROLE "${rotated}" IS 'gregale:credential:v1:${scope}:data_api';
    ALTER ROLE "${rotated}" SET statement_timeout=15000;
    GRANT CONNECT ON DATABASE "${database}" TO "${rotated}"`)
  try {
    await owner.query(`GRANT USAGE ON SCHEMA api TO "${rotated}";
      GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA api TO "${rotated}";
      GRANT SELECT, USAGE ON ALL SEQUENCES IN SCHEMA api TO "${rotated}";
      ALTER DEFAULT PRIVILEGES IN SCHEMA api GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO "${rotated}";
      ALTER DEFAULT PRIVILEGES IN SCHEMA api GRANT SELECT, USAGE ON SEQUENCES TO "${rotated}"`)
    await assert.rejects(release(migrationURL), /Exactly one active Data API role/)
    assert.equal((await release(migrationURL, { role: rotated })).role, rotated)
    await admin.query(`ALTER ROLE "${role}" NOLOGIN`)
    assert.equal((await permissions(migrationURL)).role, rotated)
    await assert.rejects(permissions(migrationURL, { role }), /Unsafe or mismatched Data API role/)
  } finally {
    await admin.query(`ALTER ROLE "${role}" LOGIN`)
    await owner.query(`REVOKE ALL ON ALL TABLES IN SCHEMA api FROM "${rotated}";
      REVOKE ALL ON ALL SEQUENCES IN SCHEMA api FROM "${rotated}";
      REVOKE ALL ON ALL FUNCTIONS IN SCHEMA api FROM "${rotated}";
      ALTER DEFAULT PRIVILEGES IN SCHEMA api REVOKE ALL ON TABLES FROM "${rotated}";
      ALTER DEFAULT PRIVILEGES IN SCHEMA api REVOKE ALL ON SEQUENCES FROM "${rotated}";
      REVOKE ALL ON SCHEMA api FROM "${rotated}"`)
    await admin.query(`REVOKE ALL ON DATABASE "${database}" FROM "${rotated}"`)
    await admin.query(`DROP ROLE "${rotated}"`)
  }
}
