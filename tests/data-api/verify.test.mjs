import test from 'node:test'
import assert from 'node:assert/strict'
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { mkdtemp, mkdir, cp, writeFile, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
const execute = promisify(execFile)
const enabled = process.env.DATA_API_VERIFY_NATIVE && process.env.DATA_API_DEV_CLI && process.env.DATA_API_STARTER_DIR

test('verify runs named readonly checks and authorization, reports failures and cleans disposable services', { skip: !enabled, timeout: 240000 }, async t => {
 const inventory = async args => (await execute('docker', args)).stdout.trim().split('\n').filter(Boolean).sort()
 const containers = () => inventory(['container', 'ls', '--all', '--filter', 'label=gregale.data-api.dev=true', '--format', '{{.Names}}'])
 const volumes = () => inventory(['volume', 'ls', '--format', '{{.Name}}'])
 const initialContainers = await containers(), initialVolumes = await volumes()
 const directory = await mkdtemp(join(tmpdir(), 'gregale-verify-test-'))
 t.after(() => rm(directory, { recursive: true, force: true }))
 await cp(process.env.DATA_API_STARTER_DIR, directory, { recursive: true })
 const output = join(directory, 'client/src/database.types.ts'), original = await readFile(output, 'utf8')
 await mkdir(join(directory, '.gregale'), { recursive: true })
 const contract = join(directory, '.gregale/data-api-contract.json')
 await writeFile(contract, 'existing-contract-preserved')
 const checks = join(directory, 'checks.json'), fixture = join(directory, 'fixture.sql')
 await writeFile(fixture, "INSERT INTO api.notes(subject,body) VALUES ('alice','private-fixture-value'),('bob','private-fixture-value');")
 const args = ['--json', 'data-api', 'verify', '--directory', directory, '--checks', checks, '--fixture', fixture]
 const run = async () => JSON.parse((await execute(process.env.DATA_API_DEV_CLI, args, { timeout: 60000 })).stdout)
 const manifest = { version: 1, checks: [{ name: 'Seed rows preserved', sql: 'SELECT count(*)=2 AS passed FROM api.notes' }, { name: 'Required bodies', sql: 'SELECT NOT EXISTS(SELECT 1 FROM api.notes WHERE body IS NULL) AS passed' }] }
 await writeFile(checks, JSON.stringify(manifest))
 const passed = await run()
 const accessFile = join(directory, 'access.json')
 const read = (name, identity, path, json) => ({ name, identity, method: 'GET', path, expect: { status: 200, json } })
 const accessManifest = { version: 1, requests: [
  read('Alice sees own row', 'alice', '/rest/v1/notes?subject=eq.alice&select=subject', [{ subject: 'alice' }]),
  read('Bob cannot see Alice', 'bob', '/rest/v1/notes?subject=eq.alice&select=subject', []),
  { name: 'Bob cannot change Alice', identity: 'bob', method: 'PATCH', path: '/rest/v1/notes?subject=eq.alice&select=subject', body: { body: 'forbidden' }, expect: { status: 200, json: [] } },
  read('Alice row unchanged', 'alice', '/rest/v1/notes?subject=eq.alice&select=body', [{ body: 'private-fixture-value' }])
 ] }
 await writeFile(accessFile, JSON.stringify(accessManifest))
 const accessArgs = ['--json', 'data-api', 'verify', '--directory', directory, '--access-checks', accessFile, '--fixture', fixture]
 const accessReport = JSON.parse((await execute(process.env.DATA_API_DEV_CLI, accessArgs, { timeout: 60000 })).stdout)
 assert.equal(accessReport.access_summary.passed, 4); assert.equal(accessReport.status, 'passed'); assert.ok(!JSON.stringify(accessReport).includes('private-fixture-value'))
 accessManifest.requests[1].expect.json = [{ subject: 'alice' }]
 await writeFile(accessFile, JSON.stringify(accessManifest))
 await assert.rejects(execute(process.env.DATA_API_DEV_CLI, accessArgs, { timeout: 60000 }), error => {
  const report = JSON.parse(error.stdout); assert.equal(report.status, 'failed'); assert.equal(report.cleanup, 'passed'); assert.equal(report.access_checks[2].skipped, true); return true
 })
 assert.equal(passed.command, 'data-api verify'); assert.equal(passed.status, 'passed'); assert.equal(passed.authorization, 'passed'); assert.equal(passed.cleanup, 'passed')
 assert.deepEqual(passed.summary, { total: 2, passed: 2, failed: 0 })
 assert.ok(!JSON.stringify(passed).includes('private-fixture-value'))
 await writeFile(checks, JSON.stringify({ version: 1, checks: [
  { name: 'False assertion', sql: 'SELECT false AS passed' },
  { name: 'Write rejected', sql: "DELETE FROM api.notes RETURNING true AS passed" },
  { name: 'Multiple statements rejected', sql: 'SELECT true AS passed; SELECT true AS passed' },
  { name: 'Wrong shape', sql: 'SELECT true AS unexpected' },
  { name: 'JSON boolean rejected', sql: "SELECT 'true'::json AS passed" },
  ...manifest.checks
 ] }))
 await assert.rejects(run(), error => {
  const report = JSON.parse(error.stdout)
  assert.equal(error.code, 1); assert.equal(report.status, 'failed'); assert.equal(report.cleanup, 'passed'); assert.equal(report.authorization, 'passed')
  assert.equal(report.summary.failed, 5); assert.ok(report.checks.slice(-2).every(check => check.passed)); return true
 })
 assert.equal(await readFile(output, 'utf8'), original)
 assert.equal(await readFile(contract, 'utf8'), 'existing-contract-preserved')
 assert.equal(await readFile(fixture, 'utf8'), "INSERT INTO api.notes(subject,body) VALUES ('alice','private-fixture-value'),('bob','private-fixture-value');")
 await writeFile(join(directory, 'migrations/sql/0017_access_membership.sql'), `
 CREATE TABLE api.access_memberships(subject text PRIMARY KEY);
 ALTER TABLE api.access_memberships ENABLE ROW LEVEL SECURITY;
 ALTER TABLE api.access_memberships FORCE ROW LEVEL SECURITY;
 CREATE POLICY own_membership ON api.access_memberships USING (subject=current_setting('request.jwt.claims',true)::jsonb->>'sub') WITH CHECK (subject=current_setting('request.jwt.claims',true)::jsonb->>'sub');
 CREATE POLICY membership_required ON api.notes AS RESTRICTIVE FOR SELECT USING (EXISTS(SELECT 1 FROM api.access_memberships m WHERE m.subject=notes.subject));
 `)
 await writeFile(fixture, (await readFile(fixture, 'utf8')) + "INSERT INTO api.access_memberships VALUES ('alice'),('bob');")
 await writeFile(accessFile, JSON.stringify({ version: 1, requests: [
  read('Member sees own notes', 'alice', '/rest/v1/notes?subject=eq.alice&select=subject', [{ subject: 'alice' }]),
  { name: 'Revoke membership', identity: 'alice', method: 'DELETE', path: '/rest/v1/access_memberships?subject=eq.alice&select=subject', expect: { status: 200, json: [{ subject: 'alice' }] } },
  read('Revoked member loses visibility', 'alice', '/rest/v1/notes?subject=eq.alice&select=subject', [])
 ] }))
 const revoked = JSON.parse((await execute(process.env.DATA_API_DEV_CLI, accessArgs, { timeout: 60000 })).stdout)
 assert.equal(revoked.status, 'passed'); assert.equal(revoked.access_summary.passed, 3)
 await writeFile(checks, '{"version":1,"checks":[]}')
 await assert.rejects(run(), error => !error.stdout)
 assert.deepEqual(await containers(), initialContainers); assert.deepEqual(await volumes(), initialVolumes)
})
