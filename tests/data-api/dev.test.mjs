import {mutationScaffold,lifecycleScaffold} from '../../cmd/gregale/templates/data-api/guided-mutations.mjs'
import test from 'node:test'
import {browserDataBrowser} from './browser-data-browser.mjs'
import {browserNoteEditor} from './browser-note-editor.mjs'
import http from 'node:http'
import assert from 'node:assert/strict'
import { spawn, execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { mkdtemp, mkdir, cp, writeFile, readFile, readdir, stat, rm, unlink, rename } from 'node:fs/promises'
import { pathToFileURL } from 'node:url'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'

const execute = promisify(execFile)
const runtime = process.env.DATA_API_RUNTIME_DIR
const starter = process.env.DATA_API_STARTER_DIR
const enabled = Boolean(runtime && starter)
const dev = runtime ? await import(pathToFileURL(join(runtime, 'dev.mjs'))) : undefined
const containers = async () => (await execute('docker', ['container', 'ls', '--all', '--filter', 'label=gregale.data-api.dev=true', '--format', '{{.Names}}'])).stdout.trim().split('\n').filter(Boolean).sort()
const volumes = async () => (await execute('docker', ['volume', 'ls', '--format', '{{.Name}}'])).stdout.trim().split('\n').filter(Boolean).sort()

test('local compatibility approvals accept exact breaks and reject stale candidates without overwriting files', { skip: !enabled, timeout: 240000 }, async t => {
  const beforeContainers = await containers(), beforeVolumes = await volumes()
  const { config, options } = await fixture(t, { check: false, json: true })
  const migrations = join(options.directory, 'migrations/sql')
  await writeFile(join(migrations, '0017_compatibility_field.sql'), 'ALTER TABLE api.notes ADD COLUMN compatibility_field integer;')
  await execute(process.execPath, [join(runtime, 'dev.mjs'), config], { timeout: 60000 })
  const baseline = join(options.directory, 'schema.json')
  const originalBaseline = await readFile(join(options.directory, '.gregale/data-api-contract.json'), 'utf8')
  await writeFile(baseline, originalBaseline)
  await writeFile(join(migrations, '0018_drop_compatibility_field.sql'), 'ALTER TABLE api.notes DROP COLUMN compatibility_field;')
  await writeFile(config, JSON.stringify({ ...options, baseline }))
  const candidate = await execute(process.execPath, [join(runtime, 'dev.mjs'), config], { timeout: 60000 })
  const report = candidate.stdout.trim().split('\n').map(line => JSON.parse(line)).find(event => event.status === 'compatibility')
  assert.equal(report.breaking, 1)
  assert.equal(report.unapproved, 1)
  const approval = join(options.directory, 'approval.json')
  const approvalContent = JSON.stringify({ version: 1, baseline_fingerprint: report.baseline_fingerprint, current_fingerprint: report.current_fingerprint,
    changes: report.changes.filter(change => change.breaking).map(({ path, reason }) => ({ path, reason, rationale: 'The client no longer uses compatibility_field.' })) })
  await writeFile(approval, approvalContent)
  await writeFile(config, JSON.stringify({ ...options, baseline, approval, check_breaking: true }))
  const accepted = await execute(options.cli, ['--json', 'data-api', 'dev', '--directory', options.directory, '--port', '0', '--once', '--baseline', 'schema.json', '--check-breaking', '--approval', 'approval.json'], { timeout: 60000 })
  const approved = accepted.stdout.trim().split('\n').map(line => JSON.parse(line)).find(event => event.status === 'compatibility')
  assert.equal(approved.breaking, 1); assert.equal(approved.approved, 1); assert.equal(approved.unapproved, 0)
  assert.equal(approved.changes[0].approved, true)
  const types = await readFile(options.output, 'utf8')
  await writeFile(join(migrations, '0019_new_candidate.sql'), 'ALTER TABLE api.notes ADD COLUMN release_label text;')
  await assert.rejects(execute(process.execPath, [join(runtime, 'dev.mjs'), config], { timeout: 60000 }), error => {
    assert.match(error.stderr, /approval fingerprints and named changes/)
    return true
  })
  assert.equal(await readFile(options.output, 'utf8'), types)
  assert.equal(await readFile(baseline, 'utf8'), originalBaseline)
  assert.equal(await readFile(approval, 'utf8'), approvalContent)
  assert.deepEqual(await containers(), beforeContainers); assert.deepEqual(await volumes(), beforeVolumes)
})

async function fixture(t, overrides = {}) {
  const directory = await mkdtemp(join(tmpdir(), 'gregale-dev-test-'))
  t.after(() => rm(directory, { recursive: true, force: true }))
  const project = join(directory, 'project')
  await cp(starter, project, { recursive: true })
  if (!overrides.starterReplay) {
    await rm(join(project, 'data-api.requests.json'), { force: true })
    await rm(join(project, 'data-api.dev.json'), { force: true })
  }
  const { starterReplay, projectSetup, ...runtimeOverrides } = overrides
  if (projectSetup) await projectSetup(project)
  const options = { directory: project, output: join(project, 'client/src/database.types.ts'), port: 0, once: true, check: true, cli: process.env.DATA_API_DEV_CLI, ...runtimeOverrides }
  const config = join(directory, 'options.json')
  await writeFile(config, JSON.stringify(options))
  return { config, options }
}

async function ordersSetup(project) {
  await rm(join(project, 'migrations'), { recursive: true })
  await cp(new URL('../../examples/data-api-orders/', import.meta.url), project, { recursive: true })
}

test('starter browser origin can use the local API while unrelated origins are refused', { skip: !enabled, timeout: 180000 }, async t => {
  const { ready, options, wait, close } = await runningFixture(t)
  const session = JSON.parse(await readFile(ready.identities_file, 'utf8'))
  const token = session.identities.alice.token
  const result = await fetch(`${ready.url}/rest/v1/notes?select=id`, {
    headers: { Origin: 'http://127.0.0.1:3030', Authorization: `Bearer ${token}` }
  })
  assert.equal(result.status, 200)
  assert.equal(result.headers.get('access-control-allow-origin'), 'http://127.0.0.1:3030')
  const refused = await fetch(`${ready.url}/rest/v1/notes?select=id`, {
    method: 'OPTIONS', headers: { Origin: 'https://unrelated.example', 'Access-Control-Request-Method': 'GET' }
  })
  assert.equal(refused.status, 403)
  assert.equal(refused.headers.get('access-control-allow-origin'), null)
  await writeFile(join(options.directory, 'migrations/sql/0017_note_label.sql'), 'ALTER TABLE api.notes ADD COLUMN label text;')
  await wait('reloaded')
  const types = await readFile(options.output, 'utf8')
  assert.match(types, /"label"\??: string \| null/)
  const expanded = await fetch(`${ready.url}/rest/v1/notes?select=id,label`, { headers: { Authorization: `Bearer ${token}` } })
  assert.equal(expanded.status, 200)
  await close()
})

test('an orders project verifies custom identities and seeds without a notes table', { skip: !enabled, timeout: 180000 }, async t => {
  const before = await containers(), beforeVolumes = await volumes()
  const { options } = await fixture(t, { check: false, projectSetup: ordersSetup, json: true })
  await rename(join(options.directory, 'data-api.dev.json'), join(options.directory, 'orders.dev.json'))
  const result = await execute(options.cli, ['--json', 'data-api', 'dev', '--directory', options.directory, '--config', 'orders.dev.json', '--port', '0', '--once'], { timeout: 150000 })
  assert.equal(JSON.parse(result.stdout.trim()).status, 'verified')
  const types = await readFile(join(options.directory, 'database.types.ts'), 'utf8')
  assert.match(types, /"orders"/); assert.doesNotMatch(types, /"notes"/)
  assert.deepEqual(await containers(), before); assert.deepEqual(await volumes(), beforeVolumes)
})

async function runningFixture(t, overrides = {}) {
  const fixtureResult = await fixture(t, { once: false, check: false, json: true, ...overrides })
  const child = spawn(process.execPath, [join(runtime, 'dev.mjs'), fixtureResult.config], { stdio: ['ignore', 'pipe', 'pipe'] })
  let pending = '', stderr = ''
  const events = []
  child.stdout.on('data', data => {
    pending += data
    for (;;) {
      const end = pending.indexOf('\n')
      if (end < 0) break
      events.push(JSON.parse(pending.slice(0, end))); pending = pending.slice(end + 1)
    }
  })
  child.stderr.on('data', data => { stderr += data })
  const completion = new Promise(resolve => child.once('exit', (code, signal) => resolve({ code, signal })))
  const close = async () => { if (child.exitCode === null) child.kill('SIGINT'); assert.deepEqual(await completion, { code: 0, signal: null }, stderr) }
  t.after(close)
  const wait = async (status, start = 0) => {
    for (let i = 0; i < 600; i++) {
      const event = events.slice(start).find(event => event.status === status)
      if (event) return event
      assert.equal(child.exitCode, null, stderr)
      await delay(100)
    }
    assert.fail(`Missing ${status} event; statuses: ${events.map(event => event.status).join(',')}; ${stderr}`)
  }
  return { ...fixtureResult, child, events, wait, close, ready: await wait('ready') }
}

test('configured authorization checks pause permissive reloads and preserve contracts', { skip: !enabled, timeout: 180000 }, async t => {
  const session = await runningFixture(t, { projectSetup: ordersSetup })
  const { options, ready, events, wait } = session
  const typesPath = join(options.directory, 'database.types.ts')
  const baseline = join(options.directory, '.gregale/data-api-contract.json')
  const types = await readFile(typesPath, 'utf8'), contract = await readFile(baseline, 'utf8')
  const identities = JSON.parse(await readFile(ready.identities_file, 'utf8')).identities
  assert.deepEqual(Object.keys(identities), ['buyer', 'other'])
  const claims = JSON.parse(Buffer.from(identities.buyer.token.split('.')[1], 'base64url'))
  assert.equal(claims.tenant, 'shop-a')
  assert.equal(claims.sub, 'customer-a')
  let mark = events.length
  await writeFile(join(options.directory, 'migrations/0002_permissive.sql'), 'ALTER POLICY own_orders ON api.orders USING (true) WITH CHECK (true);')
  assert.match((await wait('reload_failed', mark)).detail, /required project authorization verification/)
  assert.equal((await fetch(`${ready.url}/healthz`)).status, 503)
  assert.equal(await readFile(typesPath, 'utf8'), types)
  assert.equal(await readFile(baseline, 'utf8'), contract)
  mark = events.length
  await writeFile(join(options.directory, 'migrations/0003_repair.sql'), `DELETE FROM api.orders WHERE total <> 42;
    ALTER POLICY own_orders ON api.orders
      USING (owner=current_setting('request.jwt.claims',true)::jsonb->>'sub')
      WITH CHECK (owner=current_setting('request.jwt.claims',true)::jsonb->>'sub');`)
  await wait('reloaded', mark)
  assert.equal((await fetch(`${ready.url}/healthz`)).status, 200)
  await session.close()
})

test('local Docker contexts cannot redirect development services to remote hosts', { skip: !enabled }, () => {
  for (const host of ['unix:///var/run/docker.sock', 'npipe:////./pipe/docker_engine', 'tcp://127.0.0.1:2376', 'tcp://[::1]:2376']) assert.equal(dev.localDockerHost(host), true)
  for (const host of ['ssh://production', 'tcp://production:2376', 'https://localhost', undefined, '']) assert.equal(dev.localDockerHost(host), false)
})

test('local dev generates types, runs client checks and removes containers and volumes', { skip: !enabled, timeout: 180000 }, async t => {
  const before = await containers(), beforeVolumes = await volumes()
  const { config, options } = await fixture(t)
  const result = await execute(process.execPath, [join(runtime, 'dev.mjs'), config], { timeout: 150000 })
  assert.match(result.stdout, /Alice\/Bob RLS isolation verified/)
  assert.match(await readFile(options.output, 'utf8'), /Schema fingerprint: [a-f0-9]{64}/)
  assert.deepEqual(await readdir(join(options.directory, '.gregale')), ['data-api-contract.json'])
  assert.deepEqual(await containers(), before)
  assert.deepEqual(await volumes(), beforeVolumes)
})

test('local dev keeps tokens private, serves authenticated requests and cleans up on SIGINT', { skip: !enabled, timeout: 180000 }, async t => {
  const before = await containers()
  const { config, options } = await fixture(t, { once: false, check: false })
  const child = spawn(process.execPath, [join(runtime, 'dev.mjs'), config], { stdio: ['ignore', 'pipe', 'pipe'] })
  let stdout = '', stderr = ''
  child.stdout.on('data', data => { stdout += data }); child.stderr.on('data', data => { stderr += data })
  const completion = new Promise(resolve => child.once('exit', (code, signal) => resolve({ code, signal })))
  t.after(async () => { if (child.exitCode === null) child.kill('SIGINT'); await completion })
  for (let attempt = 0; attempt < 1200 && !stdout.includes('RLS isolation verified') && child.exitCode === null; attempt++) await delay(100)
  assert.match(stdout, /RLS isolation verified/, stderr)
  const file = (await readdir(join(options.directory, '.gregale'))).find(name => /^data-api-dev-[a-f0-9]{24}\.json$/.test(name))
  const path = join(options.directory, '.gregale', file)
  assert.equal((await stat(path)).mode & 0o777, 0o600)
  const receipt = JSON.parse(await readFile(path, 'utf8'))
  for (const subject of ['alice', 'bob']) {
    assert.equal(receipt.identities[subject].subject, subject)
    assert.ok(!stdout.includes(receipt.identities[subject].token))
    const result = await fetch(`${receipt.url}/rest/v1/notes`, { headers: { Authorization: `Bearer ${receipt.identities[subject].token}` } })
    assert.equal(result.status, 200)
    assert.deepEqual(await result.json(), [])
  }
  assert.equal((await fetch(`${receipt.url}/rest/v1/notes`)).status, 401)
  child.kill('SIGINT')
  assert.deepEqual(await completion, { code: 0, signal: null })
  assert.deepEqual(await readdir(join(options.directory, '.gregale')), ['data-api-contract.json'])
  assert.deepEqual(await containers(), before)
})

test('failed migrations preserve types and remove the disposable database', { skip: !enabled, timeout: 180000 }, async t => {
  const before = await containers(), beforeVolumes = await volumes()
  const { config, options } = await fixture(t, { check: false })
  await writeFile(options.output, 'existing types\n')
  await writeFile(join(options.directory, 'migrations/sql/0017_invalid.sql'), 'INVALID SQL;')
  await assert.rejects(execute(process.execPath, [join(runtime, 'dev.mjs'), config], { timeout: 150000 }), error => {
    assert.equal(error.code, 1)
    assert.match(error.stderr, /Local Data API failed/)
    return true
  })
  assert.equal(await readFile(options.output, 'utf8'), 'existing types\n')
  assert.deepEqual(await containers(), before)
  assert.deepEqual(await volumes(), beforeVolumes)
})

test('SIGTERM during startup removes a database before API readiness', { skip: !enabled, timeout: 180000 }, async t => {
  const before = await containers(), beforeVolumes = await volumes()
  const { config } = await fixture(t, { once: false, check: false })
  const child = spawn(process.execPath, [join(runtime, 'dev.mjs'), config], { stdio: ['ignore', 'pipe', 'pipe'] })
  let stdout = '', stderr = ''
  child.stdout.on('data', data => { stdout += data }); child.stderr.on('data', data => { stderr += data })
  const completion = new Promise(resolve => child.once('exit', (code, signal) => resolve({ code, signal })))
  t.after(async () => { if (child.exitCode === null) child.kill('SIGTERM'); await completion })
  for (let attempt = 0; attempt < 1200 && child.exitCode === null; attempt++) {
    if ((await containers()).some(name => !before.includes(name))) break
    await delay(100)
  }
  child.kill('SIGTERM')
  assert.deepEqual(await completion, { code: 0, signal: null }, stderr)
  assert.ok(!stdout.includes('Local Data API ready'))
  assert.deepEqual(await containers(), before)
  assert.deepEqual(await volumes(), beforeVolumes)
})

test('live migration reload preserves the session and data, rejects failures and recovers safely', { skip: !enabled, timeout: 180000 }, async t => {
  const before = await containers(), beforeVolumes = await volumes()
  const { config, options } = await fixture(t, { once: false, json: true })
  const child = spawn(process.execPath, [join(runtime, 'dev.mjs'), config], { stdio: ['ignore', 'pipe', 'pipe'] })
  let pending = '', stderr = ''
  const events = []
  child.stdout.on('data', data => {
    pending += data
    for (;;) {
      const end = pending.indexOf('\n')
      if (end === -1) break
      events.push(JSON.parse(pending.slice(0, end))); pending = pending.slice(end + 1)
    }
  })
  child.stderr.on('data', data => { stderr += data })
  const completion = new Promise(resolve => child.once('exit', (code, signal) => resolve({ code, signal })))
  t.after(async () => { if (child.exitCode === null) child.kill('SIGINT'); await completion })
  const wait = async (status, start = 0) => {
    for (let i = 0; i < 600; i++) {
      const event = events.slice(start).find(event => event.status === status)
      if (event) return event
      assert.equal(child.exitCode, null, stderr)
      await delay(100)
    }
    assert.fail(`Missing ${status} event; statuses: ${events.map(event => event.status).join(',')}; ${stderr}`)
  }
  const ready = await wait('ready')
  const firstReceipt = JSON.parse(await readFile(ready.identities_file, 'utf8'))
  const headers = { Authorization: `Bearer ${firstReceipt.identities.alice.token}`, 'Content-Type': 'application/json', Prefer: 'return=representation' }
  const request = (path, init = {}) => fetch(`${ready.url}${path}`, { headers, ...init })
  const seed = await request('/rest/v1/notes', { method: 'POST', body: JSON.stringify({ subject: 'alice', body: 'survives reload' }) })
  assert.equal(seed.status, 201)
  const [note] = await seed.json()
  const directory = join(options.directory, 'migrations/sql')
  const path = join(directory, '0017_local_reload.sql')
  const migration = 'SELECT pg_sleep(1); ALTER TABLE api.notes ADD COLUMN local_label text;'
  let mark = events.length
  await writeFile(path, migration)
  await wait('reloading', mark)
  assert.equal((await request('/healthz')).status, 503)
  await wait('reloaded', mark)
  const fresh = JSON.parse(await readFile(ready.identities_file, 'utf8'))
  assert.equal(fresh.url, ready.url)
  assert.ok(fresh.identities.alice.token === firstReceipt.identities.alice.token)
  assert.notEqual(fresh.fingerprint, firstReceipt.fingerprint)
  assert.equal(fresh.client_checked, true)
  assert.equal((await (await request('/__gregale/schema')).json()).fingerprint, fresh.fingerprint)
  assert.match(await readFile(options.output, 'utf8'), /local_label/)
  const patched = await request(`/rest/v1/notes?id=eq.${note.id}`, { method: 'PATCH', body: JSON.stringify({ local_label: 'fresh schema' }) })
  assert.equal(patched.status, 200)
  assert.equal((await patched.json())[0].body, 'survives reload')

  const goodTypes = await readFile(options.output, 'utf8')
  const pendingMigration = join(directory, '0018_pending.sql')
  mark = events.length
  await writeFile(pendingMigration, 'ALTER TABLE api.notes ADD COLUMN pending_label text; SELECT 1/0;')
  const failure = await wait('reload_failed', mark)
  assert.match(failure.detail, /SQLSTATE 22012/)
  assert.equal((await request('/healthz')).status, 503)
  assert.equal(await readFile(options.output, 'utf8'), goodTypes)
  mark = events.length
  const repaired = 'ALTER TABLE api.notes ADD COLUMN pending_label text;'
  await writeFile(pendingMigration, repaired)
  await wait('reloaded', mark)
  assert.match(await readFile(options.output, 'utf8'), /pending_label/)

  mark = events.length
  await writeFile(path, migration + '\n-- editing applied history is refused')
  assert.match((await wait('reload_failed', mark)).detail, /Restore.*applied migration.*add a new migration/)
  const failures = events.filter(event => event.status === 'reload_failed').length
  await delay(750)
  assert.equal(events.filter(event => event.status === 'reload_failed').length, failures)
  mark = events.length
  await writeFile(path, migration)
  await wait('reloaded', mark)
  mark = events.length
  await unlink(pendingMigration)
  assert.match((await wait('reload_failed', mark)).detail, /removed applied migration/)
  mark = events.length
  await writeFile(pendingMigration, repaired)
  await wait('reloaded', mark)

  // A committed unsafe schema must remain inaccessible until a subsequent
  // migration repairs it; applied history cannot be edited to pretend rollback.
  mark = events.length
  await writeFile(join(directory, '0019_unsafe.sql'), 'ALTER TABLE api.notes DISABLE ROW LEVEL SECURITY;')
  assert.match((await wait('reload_failed', mark)).detail, /RLS and RPC permission checks/)
  assert.equal((await request('/rest/v1/notes')).status, 503)
  mark = events.length
  await writeFile(join(directory, '0020_repair_and_rpc.sql'), `ALTER TABLE api.notes ENABLE ROW LEVEL SECURITY;
    CREATE FUNCTION api.dev_echo(value text) RETURNS text LANGUAGE sql SECURITY INVOKER SET search_path=pg_catalog AS 'SELECT value';
    REVOKE ALL ON FUNCTION api.dev_echo(text) FROM PUBLIC;
    COMMENT ON FUNCTION api.dev_echo(text) IS '@gregale:rpc';`)
  await wait('reloaded', mark)
  assert.match(await readFile(options.output, 'utf8'), /dev_echo/)
  const rpc = await request('/rest/v1/rpc/dev_echo', { method: 'POST', body: JSON.stringify({ value: 'new RPC' }) })
  assert.equal(rpc.status, 200)
  assert.equal(await rpc.json(), 'new RPC')
  const finalNote = await request(`/rest/v1/notes?id=eq.${note.id}`)
  assert.equal((await finalNote.json())[0].body, 'survives reload')

  const clientError = join(options.directory, 'client/src/dev-watch-error.ts')
  await writeFile(clientError, 'export const wrong: string = 123;')
  mark = events.length
  await writeFile(join(directory, '0021_client_check.sql'), 'ALTER TABLE api.notes ADD COLUMN checked_label text;')
  const failedCheck = await wait('reload_failed', mark)
  assert.match(failedCheck.detail, /client typecheck/)
  assert.equal(failedCheck.ready, true)
  assert.equal((await request('/healthz')).status, 200)
  assert.match(await readFile(options.output, 'utf8'), /checked_label/)
  const failedCheckReceipt = JSON.parse(await readFile(ready.identities_file, 'utf8'))
  assert.equal(failedCheckReceipt.status, 'reload_failed')
  assert.equal(failedCheckReceipt.client_checked, false)
  await unlink(clientError)
  mark = events.length
  await writeFile(join(directory, '0022_client_repaired.sql'), 'SELECT 1;')
  await wait('reloaded', mark)
  child.kill('SIGINT')
  assert.deepEqual(await completion, { code: 0, signal: null })
  assert.deepEqual(await containers(), before)
  assert.deepEqual(await volumes(), beforeVolumes)
})

test('default reload reports compatible additions and breaking removals before saving the new contract', { skip: !enabled, timeout: 180000 }, async t => {
  const before = await containers(), beforeVolumes = await volumes()
  const session = await runningFixture(t)
  const { options, ready, events, wait } = session
  const baseline = join(options.directory, '.gregale/data-api-contract.json')
  assert.equal((await stat(baseline)).mode & 0o777, 0o600)
  const firstContract = JSON.parse(await readFile(baseline, 'utf8'))
  assert.equal(firstContract.fingerprint, ready.fingerprint)
  const directory = join(options.directory, 'migrations/sql')
  let mark = events.length
  await writeFile(join(directory, '0017_compatible.sql'), 'ALTER TABLE api.notes ADD COLUMN local_label text;')
  const compatible = await wait('compatibility', mark)
  assert.equal(compatible.breaking, 0)
  assert.ok(compatible.changes.some(change => change.path === 'api.notes.local_label' && !change.breaking))
  await wait('reloaded', mark)
  const lastGood = JSON.parse(await readFile(baseline, 'utf8'))
  mark = events.length
  await writeFile(join(directory, '0018_breaking.sql'), 'ALTER TABLE api.notes DROP COLUMN local_label; DROP FUNCTION api.create_note_with_tags(text,text[]);')
  const breaking = await wait('compatibility', mark)
  assert.equal(breaking.baseline_fingerprint, lastGood.fingerprint)
  assert.ok(breaking.breaking >= 2)
  assert.ok(breaking.changes.some(change => change.path === 'api.notes.local_label' && change.breaking))
  assert.ok(breaking.changes.some(change => change.reason.startsWith('RPC removed') && change.breaking))
  await wait('reloaded', mark)
  const receipt = JSON.parse(await readFile(ready.identities_file, 'utf8'))
  assert.equal(receipt.compatibility.breaking, breaking.breaking)
  assert.equal((await fetch(`${ready.url}/healthz`)).status, 200)
  const saved = await readFile(baseline, 'utf8')
  assert.equal(JSON.parse(saved).fingerprint, receipt.fingerprint)
  await session.close()
  const once = { ...options, once: true }
  await writeFile(session.config, JSON.stringify(once))
  const result = await execute(process.execPath, [join(runtime, 'dev.mjs'), session.config], { timeout: 150000 })
  const reports = result.stdout.trim().split('\n').map(line => JSON.parse(line))
  assert.equal(reports.find(report => report.status === 'compatibility').breaking, 0)
  assert.deepEqual(reports.find(report => report.status === 'compatibility').changes, [])
  assert.equal(await readFile(baseline, 'utf8'), saved)
  assert.deepEqual(await containers(), before)
  assert.deepEqual(await volumes(), beforeVolumes)
})

test('strict reload preserves the accepted contract and types; pinned one-shot checks fail without overwriting baselines', { skip: !enabled, timeout: 180000 }, async t => {
  const before = await containers(), beforeVolumes = await volumes()
  const { config, options } = await fixture(t, { check: false, json: true })
  await execute(process.execPath, [join(runtime, 'dev.mjs'), config], { timeout: 150000 })
  const baseline = join(options.directory, '.gregale/data-api-contract.json')
  const pinned = join(options.directory, 'schema.json')
  const pinnedContent = await readFile(baseline, 'utf8')
  await writeFile(pinned, pinnedContent)
  const strict = { ...options, once: false, check_breaking: true }
  // Run against this initialized project, rather than a fresh fixture.
  const session = await runningFixture(t, strict)
  const { events, wait, ready } = session
  const directory = join(options.directory, 'migrations/sql')
  let mark = events.length
  await writeFile(join(directory, '0017_compatible.sql'), 'ALTER TABLE api.notes ADD COLUMN local_label text;')
  assert.equal((await wait('compatibility', mark)).breaking, 0)
  await wait('reloaded', mark)
  const accepted = await readFile(baseline, 'utf8'), types = await readFile(options.output, 'utf8')
  mark = events.length
  await writeFile(join(directory, '0018_breaking.sql'), 'ALTER TABLE api.notes DROP COLUMN priority;')
  const change = await wait('compatibility', mark)
  assert.ok(change.changes.some(change => change.path === 'api.notes.priority' && change.breaking))
  assert.match((await wait('reload_failed', mark)).detail, /Breaking schema changes rejected/)
  assert.equal((await fetch(`${ready.url}/healthz`)).status, 503)
  assert.equal(await readFile(options.output, 'utf8'), types)
  assert.equal(await readFile(baseline, 'utf8'), accepted)
  mark = events.length
  await writeFile(join(directory, '0019_repair.sql'), 'ALTER TABLE api.notes ADD COLUMN priority integer NOT NULL DEFAULT 0;')
  assert.equal((await wait('compatibility', mark)).breaking, 0)
  await wait('reloaded', mark)
  await session.close()

  const afterRepair = await readFile(baseline, 'utf8')
  const checked = { ...options, check_breaking: true, baseline: pinned }
  await writeFile(config, JSON.stringify(checked))
  await execute(process.execPath, [join(runtime, 'dev.mjs'), config], { timeout: 150000 })
  assert.equal(await readFile(pinned, 'utf8'), pinnedContent)
  assert.equal(await readFile(baseline, 'utf8'), afterRepair)
  const goodTypes = await readFile(options.output, 'utf8')
  await writeFile(join(directory, '0020_break_again.sql'), 'ALTER TABLE api.notes DROP COLUMN priority;')
  await assert.rejects(execute(process.execPath, [join(runtime, 'dev.mjs'), config], { timeout: 150000 }), error => {
    assert.equal(error.code, 1)
    assert.match(error.stderr, /Breaking schema changes rejected/)
    const reports = error.stdout.trim().split('\n').map(line => JSON.parse(line))
    assert.ok(reports.find(report => report.status === 'compatibility').breaking > 0)
    return true
  })
  assert.equal(await readFile(options.output, 'utf8'), goodTypes)
  assert.equal(await readFile(pinned, 'utf8'), pinnedContent)
  assert.equal(await readFile(baseline, 'utf8'), afterRepair)
  assert.deepEqual(await containers(), before)
  assert.deepEqual(await volumes(), beforeVolumes)
})

test('inspector protects identities and supports browser RLS requests and reload diagnostics', { skip: !enabled, timeout: 180000 }, async t => {
  const local = await runningFixture(t)
  const link = new URL(local.ready.inspector_url)
  const sessionURL = `${link.origin}${link.pathname}session`
  const headers = { Authorization: `Bearer ${link.hash.slice(1)}` }
  assert.equal((await fetch(sessionURL)).status, 401)
  assert.equal((await fetch(sessionURL, { headers: { ...headers, Origin: 'https://foreign.invalid' } })).status, 403)
  assert.equal(await new Promise((resolve, reject) => { const request = http.get(sessionURL, { headers: { ...headers, Host: 'foreign.invalid' } }, response => { response.resume(); resolve(response.statusCode) }); request.on('error', reject) }), 403)
  assert.equal((await fetch(sessionURL, { method: 'POST', headers })).status, 405)
  const state = await (await fetch(sessionURL, { headers })).json()
  assert.equal(state.ready, true)
  assert.ok(state.snapshot.tables.find(table => table.name === 'notes').columns.length)
  assert.ok(state.snapshot.functions.length)
  for(const endpoint of ['access','access-run']){
   const target=`${link.origin}${link.pathname}${endpoint}`
   assert.equal((await fetch(target)).status,401)
   assert.equal((await fetch(target,{method:'POST',headers:{...headers,Origin:'https://foreign.invalid'}})).status,403)
  }
  const accessState=await (await fetch(`${link.origin}${link.pathname}access`,{headers})).json()
  const readScaffold={version:3,status:'needs_review',view:{name:'Read-only inspector',fingerprint:state.fingerprint,steps:[{schema:'api',table:'notes',columns:['id'],filter:null,sort:null,via:null}]},fixtures:[{name:'step_1',schema:'api',table:'notes',keys:null}],checks:[{identity:'alice',steps:[{fixture:'step_1',rows:null}]},{identity:'bob',steps:[{fixture:'step_1',rows:null}]}]}
  for(const candidate of [mutationScaffold(readScaffold),lifecycleScaffold(readScaffold)])assert.equal((await fetch(`${link.origin}${link.pathname}access`,{method:'PUT',headers:{...headers,'Content-Type':'application/json','If-Match':accessState.revision},body:JSON.stringify(candidate)})).status,400)
  const viewsURL = `${link.origin}${link.pathname}views`
  assert.equal((await fetch(viewsURL)).status,401)
  assert.equal((await fetch(viewsURL,{headers:{...headers,Origin:'https://foreign.invalid'}})).status,403)
  assert.equal((await fetch(viewsURL,{method:'POST',headers})).status,405)
  const emptyViews=await (await fetch(viewsURL,{headers})).json()
  assert.deepEqual(emptyViews.views,[])
  assert.equal((await fetch(viewsURL,{method:'PUT',headers:{...headers,'Content-Type':'application/json','If-Match':emptyViews.revision},body:JSON.stringify({version:1,views:[{token:'secret'}]})})).status,400)
  const requestsURL = `${link.origin}${link.pathname}requests`
  assert.equal((await fetch(requestsURL)).status, 401)
  assert.equal((await fetch(requestsURL, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: '{}' })).status, 401)
  const empty = await (await fetch(requestsURL, { headers })).json()
  assert.deepEqual(empty.requests, [])
  assert.equal((await fetch(requestsURL, { method: 'PUT', headers: { ...headers, 'Content-Type': 'application/json', 'If-Match': empty.revision }, body: JSON.stringify({ version: 1, requests: [{ token: 'secret' }] }) })).status, 400)
  if (process.env.DATA_API_CHROMIUM_BIN) {
    const { browserInspector } = await import('./browser-inspector.mjs')
    await browserInspector(link.href, async evaluate => {
      const wait = async expression => {
        for (let i = 0; i < 200; i++) { if (await evaluate(expression)) return; await delay(50) }
        assert.fail('Inspector browser did not reach expected state')
      }
      await wait("document.getElementById('send')?.disabled === false")
      assert.equal(await evaluate('location.hash'), '')
      assert.ok(await evaluate("document.getElementById('resource').options.length > 1"))
      await evaluate("{const resources=document.getElementById('resource'); resources.value=[...resources.options].find(option=>option.textContent==='Table · api.notes').value; resources.dispatchEvent(new Event('change'))}")
      await evaluate("document.getElementById('expected-fingerprint').value='d'.repeat(64); document.getElementById('send').click()")
      await wait("document.getElementById('result-status').textContent.startsWith('409') && !document.getElementById('send').disabled")
      await evaluate(`document.getElementById('expected-fingerprint').value=${JSON.stringify(local.ready.fingerprint)}`)
      await evaluate(`document.getElementById('method').value='POST'; document.getElementById('body').value=JSON.stringify({subject:'alice',body:'<img src=x onerror=alert(1)>'}); document.getElementById('send').click()`)
      await wait("document.getElementById('result-status').textContent.startsWith('201')")
      assert.equal(await evaluate("document.getElementById('response').querySelector('img')"), null)
      await evaluate("document.getElementById('identity').value='bob'; document.getElementById('method').value='GET'; document.getElementById('send').click()")
      await wait("document.getElementById('result-status').textContent.startsWith('200') && document.getElementById('response').textContent === '[]'")
      await evaluate("{const resources=document.getElementById('resource'); resources.value=[...resources.options].find(option=>option.textContent==='RPC · api.create_note_with_tags').value; resources.dispatchEvent(new Event('change')); document.getElementById('body').value=JSON.stringify({note_body:'Inspector RPC',tag_names:['inspector']}); document.getElementById('send').click()}")
      await wait("document.getElementById('response').textContent.includes('Inspector RPC') && !document.getElementById('send').disabled")
      await evaluate("{const resources=document.getElementById('resource'); resources.value=[...resources.options].find(option=>option.textContent==='Table · api.notes').value; resources.dispatchEvent(new Event('change'))}")
      await evaluate("document.getElementById('identity').value='bob'; document.getElementById('path').value='/rest/v1/notes?subject=eq.alice'; document.getElementById('method').value='GET'; document.getElementById('request-name').value='Bob isolation'; document.getElementById('expected-status').value='200'; document.getElementById('expected-json').value='[]'; document.getElementById('save-request').click()")
      await wait("document.getElementById('saved-status').textContent === 'Saved Bob isolation.'")
      await evaluate("document.getElementById('replay').click()")
      await wait("document.getElementById('saved-status').textContent === '1/1 checks passed.'")
      await evaluate("document.getElementById('request-name').value='Expected failure'; document.getElementById('expected-status').value='403'; document.getElementById('save-request').click()")
      await wait("document.getElementById('saved-status').textContent === 'Saved Expected failure.'")
      await evaluate("document.getElementById('replay-all').click()")
      await wait("document.getElementById('saved-status').textContent === '1/2 checks passed.'")
      await evaluate("document.getElementById('expected-status').value='200'; document.getElementById('expected-json').value='[{}]'; document.getElementById('save-request').click()")
      await wait("document.getElementById('saved-status').textContent === 'Saved Expected failure.'")
      await evaluate("document.getElementById('replay').click()")
      await wait("document.getElementById('saved-status').textContent === '0/1 checks passed.' && document.getElementById('replay-results').textContent.includes('\"json_matches\": false')")
      await evaluate("document.getElementById('delete-saved').click()")
      await wait("document.getElementById('saved-status').textContent === 'Deleted Expected failure.'")
      await evaluate("document.getElementById('path').value='/rest/v1/wrong'; document.getElementById('refresh-saved').click()")
      await wait("document.getElementById('saved-status').textContent === 'Collection loaded.'")
      await evaluate("document.getElementById('load-saved').click()")
      await wait("document.getElementById('path').value === '/rest/v1/notes?subject=eq.alice'")
      assert.equal(await evaluate("document.getElementById('expected-fingerprint').value"),local.ready.fingerprint)
      await evaluate("document.getElementById('request-name').value='Capture browser note'; document.getElementById('identity').value='alice'; document.getElementById('method').value='POST'; document.getElementById('path').value='/rest/v1/notes'; document.getElementById('body').value=JSON.stringify({subject:'alice',body:'Variable browser'}); document.getElementById('expected-json').value=''; document.getElementById('expected-status').value='201'; document.getElementById('capture').value=JSON.stringify({browser_id:'/0/id'}); document.getElementById('save-request').click()")
      await wait("document.getElementById('saved-status').textContent === 'Saved Capture browser note.'")
      await evaluate("document.getElementById('load-saved').click()")
      await wait("document.getElementById('capture').value.includes('browser_id')")
      await evaluate("document.getElementById('request-name').value='Use captured ID'; document.getElementById('identity').value='bob'; document.getElementById('method').value='GET'; document.getElementById('path').value='/rest/v1/notes?id=eq.{{browser_id}}'; document.getElementById('expected-status').value='200'; document.getElementById('expected-json').value='[]'; document.getElementById('capture').value=''; document.getElementById('save-request').click()")
      await wait("document.getElementById('saved-status').textContent === 'Saved Use captured ID.'")
      await evaluate("document.getElementById('replay').click()")
      await wait("document.getElementById('replay-results').textContent.includes('Missing replay variable: browser_id')")
      await evaluate("document.getElementById('replay-all').click()")
      await wait("document.getElementById('saved-status').textContent === '3/3 checks passed.'")
      await evaluate("document.getElementById('scenario-name').value='isolated'; document.getElementById('create-scenario').click()")
      await wait("document.getElementById('saved-status').textContent === 'Created scenario isolated.'")
      await evaluate("document.getElementById('save-request').click()")
      await wait("document.getElementById('saved-status').textContent === 'Saved Use captured ID.'")
      await evaluate("document.getElementById('replay-scenarios').click()")
      await wait("document.getElementById('saved-status').textContent === '3/4 checks passed.' && document.getElementById('scenario-results').textContent.includes('isolated')")
      await evaluate("document.getElementById('delete-scenario').click()")
      await wait("document.getElementById('saved-status').textContent === 'Deleted scenario isolated.'")

      await evaluate("document.getElementById('delete-saved').click()")
      await wait("document.getElementById('saved-status').textContent === 'Deleted Use captured ID.'")
      await evaluate("document.getElementById('saved').value='Capture browser note'; document.getElementById('delete-saved').click()")
      await wait("document.getElementById('saved-status').textContent === 'Deleted Capture browser note.'")
      const start = local.events.length
      await writeFile(join(local.options.directory, 'migrations/sql/0100_inspector.sql'), 'THIS IS NOT SQL;\n')
      await local.wait('reload_failed', start)
      await wait("document.getElementById('status').textContent.includes('paused') && document.getElementById('detail').textContent.includes('SQLSTATE')")
      await writeFile(join(local.options.directory, 'migrations/sql/0100_inspector.sql'), 'ALTER TABLE api.notes ADD COLUMN inspector_example text;\n')
      await local.wait('reloaded', start)
      await wait("document.getElementById('schema').textContent.includes('inspector_example') && !document.getElementById('send').disabled")
    })
  } else assert.notEqual(process.env.DATA_API_BROWSER_REQUIRED, '1', 'Chromium required for inspector acceptance')
  const stored = process.env.DATA_API_CHROMIUM_BIN ? JSON.parse(await readFile(join(local.options.directory, 'data-api.requests.json'), 'utf8')) : { version: 1, requests: [] }
  if (process.env.DATA_API_CHROMIUM_BIN) assert.equal(stored.version === 1 ? stored.requests.length : stored.scenarios[0].requests.length, 1)
  for (const identity of Object.values(state.identities)) assert.equal(JSON.stringify(stored).includes(identity.token), false)
  await local.close()
  if (process.env.DATA_API_CHROMIUM_BIN) {
    const optionsFile = join(local.options.directory, 'restart-options.json')
    await writeFile(optionsFile, JSON.stringify({ ...local.options, once: true }))
    await execute(process.execPath, [join(runtime, 'dev.mjs'), optionsFile], { timeout: 150000 })
    assert.deepEqual(JSON.parse(await readFile(join(local.options.directory, 'data-api.requests.json'), 'utf8')), stored)
  }
})

test('CLI one-shot replay executes ordered Alice/Bob scenarios and reports failures with cleanup', { skip: !enabled, timeout: 180000 }, async t => {
  const before = await containers(), beforeVolumes = await volumes()
  const { options } = await fixture(t, { check: false })
  const file = join(options.directory, 'data-api.requests.json')
  const requests = [
    { name: 'Alice creates a note', method: 'POST', path: '/rest/v1/notes', identity: 'alice', body: { subject: 'alice', body: 'Replay note' }, expect: { status: 201 }, capture: { note_id: '/0/id' } },
    { name: 'Bob cannot read Alice', method: 'GET', path: '/rest/v1/notes?id=eq.{{note_id}}', identity: 'bob', expect: { status: 200, json: [] } },
    { name: 'Bob cannot update Alice', method: 'PATCH', path: '/rest/v1/notes?id=eq.{{note_id}}', identity: 'bob', body: { body: 'Forbidden' }, expect: { status: 200, json: [] } },
    { name: 'Alice updates her note', method: 'PATCH', path: '/rest/v1/notes?id=eq.{{note_id}}&select=id,body', identity: 'alice', body: { body: 'Updated' }, expect: { status: 200, json: [{ id: '{{note_id}}', body: 'Updated' }] } },
    { name: 'Alice reads updated note', method: 'GET', path: '/rest/v1/notes?id=eq.{{note_id}}&select=id,body', identity: 'alice', expect: { status: 200, json: [{ body: 'Updated', id: '{{note_id}}' }] } },
    { name: 'Alice deletes her note', method: 'DELETE', path: '/rest/v1/notes?id=eq.{{note_id}}&select=id', identity: 'alice', expect: { status: 200, json: [{ id: '{{note_id}}' }] } },
    { name: 'Deleted note is gone', method: 'GET', path: '/rest/v1/notes?id=eq.{{note_id}}', identity: 'alice', expect: { status: 200, json: [] } }
  ]
  const content = JSON.stringify({ version: 1, requests })
  await writeFile(file, content)
  const run = async () => {
    try { return await execute(process.env.DATA_API_DEV_CLI, ['--json', 'data-api', 'dev', '--directory', options.directory, '--port', '0', '--once', '--replay', 'data-api.requests.json'], { timeout: 150000 }) }
    catch (error) { return error }
  }
  const success = await run()
  assert.equal(success.code, undefined, success.stderr)
  const events = success.stdout.trim().split('\n').map(line => JSON.parse(line))
  assert.deepEqual(events.filter(event => event.event === 'replay_result').map(event => event.name), requests.map(request => request.name))
  assert.deepEqual(events.find(event => event.event === 'replay_summary'), { event: 'replay_summary', total: 7, passed: 7, failed: 0 })
  assert.equal(events.at(-1).status, 'verified')
  assert.equal(await readFile(file, 'utf8'), content)
  assert.deepEqual(await containers(), before); assert.deepEqual(await volumes(), beforeVolumes)
  const grouped = { version: 2, scenarios: [{ name: 'notes-rls', requests }, { name: 'isolated', requests: [{ ...requests[1], name: 'No leaked ID' }] }] }
  await writeFile(file, JSON.stringify(grouped))
  const all = await run()
  assert.equal(all.code, 1)
  const allEvents = all.stdout.trim().split('\n').map(line => JSON.parse(line))
  assert.deepEqual(allEvents.filter(event => event.event === 'replay_scenario_summary').map(event => ({ scenario: event.scenario, passed: event.passed, failed: event.failed })), [{ scenario: 'notes-rls', passed: 7, failed: 0 }, { scenario: 'isolated', passed: 0, failed: 1 }])
  assert.match(allEvents.find(event => event.scenario === 'isolated' && event.event === 'replay_result').error, /Missing replay variable/)
  const selected = await execute(process.env.DATA_API_DEV_CLI, ['--json', 'data-api', 'dev', '--directory', options.directory, '--port', '0', '--once', '--replay', 'data-api.requests.json', '--scenario', 'notes-rls'], { timeout: 150000 })
  const selectedEvents = selected.stdout.trim().split('\n').map(line => JSON.parse(line))
  assert.equal(selectedEvents.filter(event => event.event === 'replay_scenario_summary').length, 1)
  assert.equal(selectedEvents.some(event => event.scenario === 'isolated'), false)
  assert.equal(selectedEvents.at(-1).status, 'verified')
  let unknown
  try { await execute(process.env.DATA_API_DEV_CLI, ['--json', 'data-api', 'dev', '--directory', options.directory, '--once', '--replay', 'data-api.requests.json', '--scenario', 'unknown'], { timeout: 150000 }) } catch (error) { unknown = error }
  assert.equal(unknown.code, 1); assert.match(unknown.stderr, /Unknown replay scenario/)
  assert.deepEqual(await containers(), before); assert.deepEqual(await volumes(), beforeVolumes)
  const independent = { ...requests[1], path: '/rest/v1/notes?subject=eq.alice' }
  await writeFile(file, JSON.stringify({ version: 1, requests: [{ ...independent, name: 'Bad status', expect: { status: 403 } }, { ...independent, name: 'Bad JSON', expect: { status: 200, json: [{}] } }, independent] }))
  const failed = await run()
  assert.equal(failed.code, 1)
  const failures = failed.stdout.trim().split('\n').map(line => JSON.parse(line))
  assert.deepEqual(failures.find(event => event.event === 'replay_summary'), { event: 'replay_summary', total: 3, passed: 1, failed: 2 })
  assert.equal(failures.some(event => event.status === 'verified'), false)
  assert.equal(failures.filter(event => event.event === 'replay_result')[1].json_matches, false)
  assert.deepEqual(await containers(), before); assert.deepEqual(await volumes(), beforeVolumes)
  await writeFile(file, '{"version":1,"requests":[{"token":"secret"}]}')
  const invalid = await run()
  assert.equal(invalid.code, 1); assert.equal(invalid.stdout.includes('replay_result'), false)
  assert.deepEqual(await containers(), before); assert.deepEqual(await volumes(), beforeVolumes)
  await writeFile(file, content)
  const interrupted = spawn(process.env.DATA_API_DEV_CLI, ['--json', 'data-api', 'dev', '--directory', options.directory, '--port', '0', '--once', '--replay', 'data-api.requests.json'], { stdio: ['ignore', 'pipe', 'pipe'] })
  let output = ''; interrupted.stdout.on('data', data => { output += data }); interrupted.stderr.resume()
  const completion = new Promise(resolve => interrupted.once('exit', (code, signal) => resolve({ code, signal })))
  t.after(async () => { if (interrupted.exitCode === null) interrupted.kill('SIGINT'); await completion })
  for (let i = 0; i < 600; i++) {
    assert.equal(interrupted.exitCode, null)
    if ((await containers()).some(name => !before.includes(name))) break
    await delay(100)
  }
  interrupted.kill('SIGINT')
  assert.deepEqual(await completion, { code: 1, signal: null })
  assert.equal(output.includes('replay_summary'), false)
  assert.deepEqual(await containers(), before); assert.deepEqual(await volumes(), beforeVolumes)

})


test('shipped starter collection passes all workflows repeatedly and cleans its data', { skip: !enabled, timeout: 180000 }, async t => {
  const before = await containers(), beforeVolumes = await volumes()
  const local = await runningFixture(t, { starterReplay: true })
  const saved = await readFile(join(local.options.directory, 'data-api.requests.json'), 'utf8')
  const collection = JSON.parse(saved)
  const { validateRequests } = await import(pathToFileURL(join(runtime, 'dev-requests.mjs')))
  const { replayRequest, collectionScenarios } = await import(pathToFileURL(join(runtime, 'request-replay.mjs')))
  validateRequests(collection)
  assert.deepEqual(collection.scenarios.map(scenario => scenario.name), ['notes-crud', 'notes-rls', 'notes-rpc'])
  const session = JSON.parse(await readFile(local.ready.identities_file, 'utf8'))
  for (let repetition = 0; repetition < 2; repetition++) {
    for (const scenario of collectionScenarios(collection)) {
      const variables = new Map()
      for (const request of scenario.requests) {
        const result = await replayRequest(request, { url: local.ready.url, identities: session.identities, variables })
        assert.equal(result.passed, true, `${scenario.name}/${request.name}: ${JSON.stringify(result)}`)
      }
    }
    for (const table of ['notes', 'tags', 'note_tags']) {
      const response = await fetch(`${local.ready.url}/rest/v1/${table}`, { headers: { Authorization: `Bearer ${session.identities.alice.token}` } })
      assert.equal(response.status, 200); assert.deepEqual(await response.json(), [])
    }
  }
  assert.equal(await readFile(join(local.options.directory, 'data-api.requests.json'), 'utf8'), saved)
  await local.close()
  const result = await execute(process.env.DATA_API_DEV_CLI, ['--json', 'data-api', 'dev', '--directory', local.options.directory, '--port', '0', '--once', '--replay', 'data-api.requests.json'], { timeout: 150000 })
  const events = result.stdout.trim().split('\n').map(line => JSON.parse(line))
  const count = collection.scenarios.reduce((sum, scenario) => sum + scenario.requests.length, 0)
  assert.deepEqual(events.find(event => event.event === 'replay_summary'), { event: 'replay_summary', total: count, passed: count, failed: 0 })
  assert.equal(events.filter(event => event.event === 'replay_scenario_summary').length, 3)
  assert.equal(events.at(-1).status, 'verified')
  assert.deepEqual(await containers(), before); assert.deepEqual(await volumes(), beforeVolumes)
})


test('local CI checks committed types without rewriting stale or missing files', { skip: !enabled, timeout: 600000 }, async t => {
  const before = await containers(), beforeVolumes = await volumes()
  const { config, options } = await fixture(t, { check: false, json: true })
  await execute(process.execPath, [join(runtime, 'dev.mjs'), config], { timeout: 150000 })
  const original = await readFile(options.output, 'utf8')
  await writeFile(config, JSON.stringify({ ...options, check_types: true }))
  const matching = await execute(options.cli, ['--json', 'data-api', 'dev', '--directory', options.directory, '--port', '0', '--once', '--check-types'], { timeout: 150000 })
  assert.equal(matching.stdout.trim().split('\n').filter(line => line.startsWith('{')).map(line => JSON.parse(line)).find(value => value.types_checked)?.types_checked, true)
  assert.equal(await readFile(options.output, 'utf8'), original)
  await writeFile(join(options.directory, 'migrations/sql/0017_ci_drift.sql'), 'ALTER TABLE api.notes ADD COLUMN ci_extra text;\n')
  await assert.rejects(execute(process.execPath, [join(runtime, 'dev.mjs'), config], { timeout: 150000 }), error => /types are stale.*regenerate/.test(error.stderr))
  assert.equal(await readFile(options.output, 'utf8'), original)
  await writeFile(options.output, 'stale committed types\n')
  await assert.rejects(execute(process.execPath, [join(runtime, 'dev.mjs'), config], { timeout: 150000 }), error => /types are stale.*regenerate/.test(error.stderr))
  assert.equal(await readFile(options.output, 'utf8'), 'stale committed types\n')
  await unlink(options.output)
  await assert.rejects(execute(process.execPath, [join(runtime, 'dev.mjs'), config], { timeout: 150000 }), error => /types are missing.*regenerate/.test(error.stderr))
  await assert.rejects(readFile(options.output), { code: 'ENOENT' })
  assert.deepEqual(await containers(), before); assert.deepEqual(await volumes(), beforeVolumes)
})

test('local contract policies replay mixed clients, revoke acceptance and pause stale reloads without losing the session', { skip: !enabled, timeout: 300000 }, async t => {
  const beforeContainers = await containers(), beforeVolumes = await volumes()
  const { config, options } = await fixture(t, { check: false, json: true })
  await execute(process.execPath, [join(runtime,'dev.mjs'),config], {timeout:60000})
  const oldPath = join(options.directory,'old.json')
  const oldContent = await readFile(join(options.directory,'.gregale/data-api-contract.json'),'utf8')
  const older = JSON.parse(oldContent).fingerprint
  await writeFile(oldPath,oldContent)
  const addition = 'ALTER TABLE api.notes ADD COLUMN rollout_label text;'
  await writeFile(join(options.directory,'migrations/sql/0017_rollout.sql'),addition)
  await execute(process.execPath, [join(runtime,'dev.mjs'),config], {timeout:60000})
  const currentPath = join(options.directory,'.gregale/data-api-contract.json')
  const current = JSON.parse(await readFile(currentPath,'utf8')).fingerprint
  const policyPath = join(options.directory,'accepted.json')
  await execute(options.cli,['data-api','diff','--baseline',oldPath,'--current',currentPath,'--accept-contract',policyPath],{timeout:60000})
  const validPolicy = await readFile(policyPath,'utf8')
  const retainedTypes = await readFile(options.output,'utf8'), retainedContract = await readFile(currentPath,'utf8')
  await writeFile(policyPath,JSON.stringify({version:1,current_fingerprint:older,accepted_fingerprints:[current]}))
  await assert.rejects(execute(options.cli,['data-api','dev','--directory',options.directory,'--port','0','--once','--accepted-contracts','accepted.json'],{timeout:60000}), error => /Accepted contracts are stale/.test(error.stderr))
  assert.equal(await readFile(options.output,'utf8'),retainedTypes)
  assert.equal(await readFile(currentPath,'utf8'),retainedContract)
  await writeFile(policyPath,validPolicy)
  const rejected = 'd'.repeat(64)
  const pinned = (name,hash,identity,method,path,expect,extra={}) => ({name,expectedSchemaFingerprint:hash,identity,method,path,expect,...extra})
  const requests = [
    pinned('old writes',older,'alice','POST','/rest/v1/notes',{status:201},{body:{subject:'alice',body:'rollout-replay'},capture:{id:'/0/id'}}),
    pinned('current respects RLS',current,'bob','GET','/rest/v1/notes?id=eq.{{id}}',{status:200,json:[]}),
    pinned('unapproved write rejected',rejected,'alice','POST','/rest/v1/notes',{status:409},{body:{subject:'alice',body:'must-not-write'}}),
    pinned('old RPC',older,'alice','POST','/rest/v1/rpc/create_note_with_tags',{status:200},{body:{note_body:'old-rpc',tag_names:[]}}),
    pinned('old deletes',older,'alice','DELETE','/rest/v1/notes?id=eq.{{id}}',{status:200}),
  ]
  await writeFile(join(options.directory,'rollout.requests.json'),JSON.stringify({version:2,scenarios:[{name:'mixed',requests}]}))
  const replay = await execute(options.cli,['--json','data-api','dev','--directory',options.directory,'--port','0','--once','--accepted-contracts','accepted.json','--replay','rollout.requests.json'],{timeout:60000})
  assert.equal(replay.stdout.trim().split('\n').map(line=>JSON.parse(line)).find(event=>event.event==='replay_summary').failed,0)
  const session = await runningFixture(t,{accepted_contracts:policyPath,projectSetup:project=>writeFile(join(project,'migrations/sql/0017_rollout.sql'),addition)})
  const identities = JSON.parse(await readFile(session.ready.identities_file,'utf8')).identities
  const headers = {Authorization:`Bearer ${identities.alice.token}`}
  const route = hash => `${session.ready.url}/__gregale/contract/${hash}/rest/v1/notes?select=id`
  assert.equal((await fetch(route(older),{headers})).status,200)
  const types = await readFile(session.options.output,'utf8')
  const contractPath = join(session.options.directory,'.gregale/data-api-contract.json')
  const contract = await readFile(contractPath,'utf8')
  let mark = session.events.length
  await writeFile(policyPath,JSON.stringify({version:1,current_fingerprint:current,accepted_fingerprints:[rejected]}))
  await session.wait('reloaded',mark)
  assert.equal((await fetch(route(older),{headers})).status,409)
  assert.equal((await fetch(route(current),{headers})).status,200)
  mark = session.events.length
  await writeFile(join(session.options.directory,'migrations/sql/0018_stale.sql'),'ALTER TABLE api.notes ADD COLUMN future_label text;')
  const failed = await session.wait('reload_failed',mark)
  assert.match(failed.detail,/Accepted contracts are stale/)
  assert.equal((await fetch(route(current),{headers})).status,503)
  assert.equal(await readFile(session.options.output,'utf8'),types)
  assert.equal(await readFile(contractPath,'utf8'),contract)
  const receipt = JSON.parse(await readFile(session.ready.identities_file,'utf8'))
  assert.deepEqual(receipt.identities,identities)
  // Inspect the migrated schema in an independent disposable run without rewriting the retained files.
  const fixedBaseline = join(session.options.directory,'fixed.json');await writeFile(fixedBaseline,contract)
  const candidate = await execute(options.cli,['--json','data-api','dev','--directory',session.options.directory,'--port','0','--once','--baseline','fixed.json','--output','candidate.types.ts'],{timeout:60000})
  const next = candidate.stdout.trim().split('\n').map(line=>JSON.parse(line)).find(event=>event.status==='verified').fingerprint
  mark = session.events.length
  await writeFile(policyPath,JSON.stringify({version:1,current_fingerprint:next,accepted_fingerprints:[older]}))
  await session.wait('reloaded',mark)
  assert.equal((await fetch(route(older),{headers})).status,200)
  assert.equal((await fetch(route(next),{headers})).status,200)
  assert.equal((await fetch(route(current),{headers})).status,409)
  assert.deepEqual(JSON.parse(await readFile(session.ready.identities_file,'utf8')).identities,identities)
  await session.close()
  assert.deepEqual(await containers(),beforeContainers);assert.deepEqual(await volumes(),beforeVolumes)
})

test('migration preview aggregates checks, preserves inputs, and removes disposable services on success and failure', { skip: !enabled, timeout: 360000 }, async t => {
  const beforeContainers = await containers(), beforeVolumes = await volumes()
  const { config, options } = await fixture(t, { json: true })
  await writeFile(join(options.directory, 'migrations/sql/0017_preview_field.sql'), 'ALTER TABLE api.notes ADD COLUMN preview_field text;')
  await execute(process.execPath, [join(runtime, 'dev.mjs'), config], { timeout: 90000 })
  const baseline = join(options.directory, 'baseline.json')
  const originalBaseline = await readFile(join(options.directory, '.gregale/data-api-contract.json'), 'utf8')
  await writeFile(baseline, originalBaseline)
  const originalTypes = await readFile(options.output, 'utf8')
  const deps = join(options.directory, 'dependencies.json')
  const dependencyContent = JSON.stringify({ version: 1, consumers: [{ kind: 'saved_view', name: 'Notes view', dependencies: ['api.notes'] }] })
  await writeFile(deps, dependencyContent)
  const replay = join(options.directory, 'preview-requests.json')
  const replayContent = JSON.stringify({ version: 1, requests: [{ name: 'Preview read', method: 'GET', path: '/rest/v1/notes?select=id', identity: 'alice', expect: { status: 200 } }] })
  await writeFile(replay, replayContent)
  const args = ['--json', 'data-api', 'migration', 'preview', '--directory', options.directory, '--baseline', 'baseline.json', '--dependencies', 'dependencies.json', '--replay', 'preview-requests.json']
  const success = await execute(options.cli, args, { timeout: 120000 })
  const report = JSON.parse(success.stdout)
  assert.equal(report.command, 'data-api migration preview'); assert.equal(report.mode, 'clean_rebuild'); assert.equal(report.status, 'passed')
  for (const step of Object.values(report.steps)) assert.equal(step.status, 'passed')
  assert.ok(report.migrations.length); assert.match(report.migrations[0].checksum, /^[a-f0-9]{64}$/)
  assert.equal(report.compatibility.baseline_fingerprint, JSON.parse(originalBaseline).fingerprint)
  assert.equal(report.compatibility.breaking, 0); assert.equal(report.replay_summary.passed, 1)
  assert.equal(report.dependency_coverage, 'declared'); assert.deepEqual(report.compatibility.impacts, [])
  assert.equal('identities' in report, false); assert.equal('identities_file' in report, false)
  const assertPreserved = async () => {
    assert.equal(await readFile(baseline, 'utf8'), originalBaseline)
    assert.equal(await readFile(join(options.directory, '.gregale/data-api-contract.json'), 'utf8'), originalBaseline)
    assert.equal(await readFile(options.output, 'utf8'), originalTypes)
    assert.equal(await readFile(deps, 'utf8'), dependencyContent)
    assert.deepEqual(await containers(), beforeContainers); assert.deepEqual(await volumes(), beforeVolumes)
    assert.equal((await readdir(join(options.directory, '.gregale'))).some(name => name.startsWith('data-api-dev-')), false)
  }
  await assertPreserved()
  const humanOptions = { ...options, preview: true, baseline, dependencies: deps, check_types: true, check_breaking: true, json: false }
  await writeFile(config, JSON.stringify(humanOptions))
  const human = await execute(process.execPath, [join(runtime, 'dev.mjs'), config], { timeout: 90000 })
  assert.match(human.stdout, /Migration preview: passed/); assert.match(human.stdout, /replay: skipped/)
  assert.match(human.stdout, /Impact coverage: declared/)
  await assertPreserved()
  await writeFile(join(options.directory, 'migrations/sql/0018_preview_drop.sql'), 'ALTER TABLE api.notes DROP COLUMN preview_field;')
  await writeFile(replay, JSON.stringify({ version: 1, requests: [{ name: 'Preview mismatch', method: 'GET', path: '/rest/v1/notes?select=id', identity: 'alice', expect: { status: 201 } }] }))
  let failed
  await assert.rejects(execute(options.cli, args, { timeout: 120000 }), error => { failed = JSON.parse(error.stdout); return error.code === 1 })
  assert.equal(failed.status, 'failed'); assert.equal(failed.steps.compatibility.status, 'failed'); assert.equal(failed.steps.types.status, 'failed')
  assert.equal(failed.steps.client.status, 'passed'); assert.equal(failed.steps.replay.status, 'failed'); assert.equal(failed.steps.cleanup.status, 'passed')
  assert.equal(failed.compatibility.impacts[0].name, 'Notes view'); assert.equal(failed.compatibility.unapproved, 1)
  assert.equal(failed.replay_results[0].name, 'Preview mismatch'); assert.equal(failed.replay_summary.failed, 1)
  await assertPreserved()
  const approval = join(options.directory, 'preview-approval.json')
  const approvalContent = JSON.stringify({ version: 1, baseline_fingerprint: failed.compatibility.baseline_fingerprint, current_fingerprint: failed.compatibility.current_fingerprint,
    changes: failed.compatibility.changes.filter(change => change.breaking).map(({ path, reason }) => ({ path, reason, rationale: 'The view no longer uses preview_field.' })) })
  await writeFile(approval, approvalContent)
  const failingTest = join(options.directory, 'client/src/preview-failure.ts')
  await writeFile(failingTest, "export const previewFailure: never = 1;\n")
  const approvedArgs = [...args.slice(0, -2), '--approval', 'preview-approval.json']
  await assert.rejects(execute(options.cli, approvedArgs, { timeout: 120000 }), error => {
    const approved = JSON.parse(error.stdout)
    assert.equal(approved.steps.compatibility.status, 'passed'); assert.equal(approved.compatibility.approved, 1)
    assert.equal(approved.steps.types.status, 'failed'); assert.equal(approved.steps.client.status, 'failed')
    assert.equal(approved.steps.replay.status, 'skipped'); assert.equal(approved.steps.cleanup.status, 'passed')
    assert.equal(approved.status, 'failed'); return error.code === 1
  })
  await unlink(failingTest)
  assert.equal(await readFile(approval, 'utf8'), approvalContent)
  await assertPreserved()
  // Invalid SQL still yields a report and cleans up before returning a failing exit.
  await writeFile(join(options.directory, 'migrations/sql/0019_preview_invalid.sql'), 'SELECT preview_nonexistent_function();')
  await assert.rejects(execute(options.cli, args, { timeout: 120000 }), error => {
    const invalid = JSON.parse(error.stdout)
    assert.equal(invalid.steps.migrations.status, 'failed'); assert.equal(invalid.steps.compatibility.status, 'not_run'); assert.equal(invalid.steps.cleanup.status, 'passed')
    assert.equal(invalid.status, 'failed'); return error.code === 1
  })
  await assertPreserved()
})

test('upgrade preview loads older-schema fixtures, applies pending migrations, and checks preserved data', { skip: !enabled, timeout: 360000 }, async t => {
  const beforeContainers = await containers(), beforeVolumes = await volumes()
  const { config, options } = await fixture(t, { json: true })
  await execute(process.execPath, [join(runtime, 'dev.mjs'), config], { timeout: 90000 })
  const baseline = join(options.directory, 'source.json')
  const source = await readFile(join(options.directory, '.gregale/data-api-contract.json'), 'utf8')
  await writeFile(baseline, source)
  const migrations = join(options.directory, 'migrations/sql')
  await writeFile(join(migrations, '0017_upgrade_backfill.sql'), "ALTER TABLE api.notes ADD COLUMN label text; UPDATE api.notes SET body=upper(body),label='migrated';")
  // Commit candidate types through ordinary development before the read-only rehearsal.
  await execute(process.execPath, [join(runtime, 'dev.mjs'), config], { timeout: 90000 })
  const originalTypes = await readFile(options.output, 'utf8')
  const candidate = await readFile(join(options.directory, '.gregale/data-api-contract.json'), 'utf8')
  const fixtureSQL = "INSERT INTO api.notes(subject,body) VALUES ('fixture-user','older A'),('fixture-user','older B'); INSERT INTO api.comments(subject,note_id,body) VALUES ('fixture-user',1,'linked data');"
  const fixturePath = join(options.directory, 'older.sql')
  await writeFile(fixturePath, fixtureSQL)
  const checksPath = join(options.directory, 'data-checks.json')
  const checks = [
    { name: 'Rows and identities preserved', sql: "SELECT count(*)=2 AND min(id)=1 AND max(id)=2 AS passed FROM api.notes WHERE subject='fixture-user'" },
    { name: 'Existing values backfilled', sql: "SELECT count(*)=2 AND bool_and(body IN ('OLDER A','OLDER B') AND label='migrated') AS passed FROM api.notes WHERE subject='fixture-user'" },
    { name: 'Related rows preserved', sql: "SELECT count(*)=1 AS passed FROM api.comments c JOIN api.notes n ON n.id=c.note_id WHERE c.subject='fixture-user' AND c.body='linked data'" },
  ]
  await writeFile(checksPath, JSON.stringify({ version: 1, checks }))
  const args = ['--json', 'data-api', 'migration', 'preview', '--directory', options.directory, '--baseline', 'source.json', '--from-version', '16', '--fixture', 'older.sql', '--data-check', 'data-checks.json']
  const result = await execute(options.cli, args, { timeout: 120000 })
  const report = JSON.parse(result.stdout)
  assert.equal(report.status, 'passed'); assert.equal(report.mode, 'upgrade'); assert.equal(report.upgrade.from_version, 16)
  assert.equal(report.upgrade.source_fingerprint, JSON.parse(source).fingerprint)
  assert.equal(report.upgrade.source_migrations.length, 16); assert.deepEqual(report.upgrade.pending_migrations.map(row => row.version), [17])
  assert.equal(report.migrations.length, 17); assert.equal(report.data_checks.length, 3)
  for (const check of report.data_checks) assert.equal(check.passed, true)
  for (const [name, step] of Object.entries(report.steps)) assert.equal(step.status, name === 'replay' ? 'skipped' : 'passed')
  assert.equal(report.data_check_coverage, 'declared'); assert.ok(!result.stdout.includes('older A')); assert.ok(!result.stdout.includes('fixture-user'))
  const assertPreserved = async () => {
    assert.equal(await readFile(baseline, 'utf8'), source)
    assert.equal(await readFile(join(options.directory, '.gregale/data-api-contract.json'), 'utf8'), candidate)
    assert.equal(await readFile(options.output, 'utf8'), originalTypes)
    assert.equal(await readFile(fixturePath, 'utf8'), fixtureSQL)
    assert.deepEqual(await containers(), beforeContainers); assert.deepEqual(await volumes(), beforeVolumes)
  }
  await assertPreserved()
  // Failed/read-only/multi-statement assertions report failure and preserve later checks.
  await writeFile(checksPath, JSON.stringify({ version: 1, checks: [
    { name: 'Expected false', sql: 'SELECT false AS passed' },
    { name: 'Writes denied', sql: "UPDATE api.notes SET body='corrupted' RETURNING true AS passed" },
    { name: 'Multiple statements denied', sql: "COMMIT; DELETE FROM api.notes; SELECT true AS passed" },
    ...checks,
  ] }))
  await assert.rejects(execute(options.cli, args, { timeout: 120000 }), error => {
    const failed = JSON.parse(error.stdout)
    assert.equal(failed.status, 'failed'); assert.equal(failed.steps.data.status, 'failed', JSON.stringify(failed.steps))
    assert.deepEqual(failed.data_checks.map(check => check.passed), [false, false, false, true, true, true])
    assert.equal(failed.steps.authorization.status, 'passed'); assert.equal(failed.steps.client.status, 'passed'); assert.equal(failed.steps.cleanup.status, 'passed')
    assert.ok(!error.stdout.includes('corrupted')); return error.code === 1
  })
  await writeFile(checksPath, JSON.stringify({ version: 1, checks }))
  await assertPreserved()
  // The source schema must match the chosen baseline, independently of approvals.
  const mismatchedArgs = args.map(value => value === 'source.json' ? '.gregale/data-api-contract.json' : value)
  await assert.rejects(execute(options.cli, mismatchedArgs, { timeout: 120000 }), error => {
    const mismatch = JSON.parse(error.stdout)
    assert.equal(mismatch.steps.source.status, 'failed'); assert.equal(mismatch.steps.fixture.status, 'not_run')
    assert.equal(mismatch.steps.migrations.status, 'not_run'); assert.equal(mismatch.steps.cleanup.status, 'passed')
    assert.match(mismatch.steps.source.detail, /does not match/); return error.code === 1
  })
  await assertPreserved()
  // An upgrade can fail on existing rows although rebuilding an empty schema succeeds.
  await writeFile(join(migrations, '0018_upgrade_constraint.sql'), "ALTER TABLE api.notes ADD CONSTRAINT fixture_constraint CHECK(body <> 'OLDER A');")
  await assert.rejects(execute(options.cli, args, { timeout: 120000 }), error => {
    const invalid = JSON.parse(error.stdout)
    assert.equal(invalid.steps.source.status, 'passed'); assert.equal(invalid.steps.fixture.status, 'passed')
    assert.equal(invalid.steps.migrations.status, 'failed'); assert.equal(invalid.steps.data.status, 'not_run'); assert.equal(invalid.steps.cleanup.status, 'passed')
    assert.deepEqual(invalid.upgrade.pending_migrations.map(row => row.version), [17, 18])
    assert.equal(invalid.migrations.length, 16); assert.ok(!error.stdout.includes('OLDER A')); return error.code === 1
  })
  await assertPreserved()
  const clean = await execute(options.cli, ['--json', 'data-api', 'migration', 'preview', '--directory', options.directory, '--baseline', 'source.json'], { timeout: 120000 })
  assert.equal(JSON.parse(clean.stdout).status, 'passed')
  await assertPreserved()
  // Future-column fixtures fail at loading, before pending migrations execute.
  const futureFixture = join(options.directory, 'future.sql')
  await writeFile(futureFixture, "INSERT INTO api.notes(subject,body,label) VALUES ('fixture-user','future','missing');")
  await assert.rejects(execute(options.cli, args.map(value => value === 'older.sql' ? 'future.sql' : value), { timeout: 120000 }), error => {
    const invalid = JSON.parse(error.stdout)
    assert.equal(invalid.steps.fixture.status, 'failed'); assert.equal(invalid.steps.migrations.status, 'not_run')
    assert.equal(invalid.steps.cleanup.status, 'passed'); return error.code === 1
  })
  await assertPreserved()
})

test('opt-in validators refresh on migrations, prevent form transport and support full-output checks', {skip:!enabled,timeout:180000},async t=>{
  const before=await containers(),beforeVolumes=await volumes()
  const {ready,options,wait,close}=await runningFixture(t,{validators:true})
  assert.match(await readFile(options.output,'utf8'),/export const inputValidators/)
  await writeFile(join(options.directory,'migrations/sql/0017_body_length.sql'),"ALTER TABLE api.notes ALTER COLUMN body TYPE varchar(128); COMMENT ON COLUMN api.notes.version IS '@gregale:version'; CREATE FUNCTION api.csv_skip_fixture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.body='inspector-csv-skipped' THEN RETURN NULL; END IF; RETURN NEW; END $$; CREATE TRIGGER csv_skip_fixture BEFORE INSERT ON api.notes FOR EACH ROW EXECUTE FUNCTION api.csv_skip_fixture();")
  await wait('reloaded')
  const generated=await readFile(options.output,'utf8')
  assert.match(generated,/"maxLength":128/)
  // Load the starter form against the just-generated module, with no live transport.
  for(const name of ['notes.ts','note-batch.ts','note-changes.ts','note-search.ts','note-form.ts','csv.ts','note-import.ts','schema-form.ts']) await cp(new URL(`../../cmd/gregale/templates/data-api-starter/client/src/${name}`,import.meta.url),join(options.directory,'client/src',name))
  await execute('npm',['run','build'],{cwd:join(options.directory,'client'),timeout:30000})
  const {notesClient}=await import(pathToFileURL(join(options.directory,'client/dist/notes.js')))
  const {createNoteForm}=await import(pathToFileURL(join(options.directory,'client/dist/note-form.js')))
  const {noteFormFields}=await import(pathToFileURL(join(options.directory,'client/dist/schema-form.js')))
  assert.equal(noteFormFields('insert').find(f=>f.name==='body').maxLength,128)
  assert.equal(noteFormFields('update').find(f=>f.name==='body').required,false)
  assert.ok(noteFormFields('update').every(f=>['body','priority'].includes(f.name)))
  const {previewNotesCsv}=await import(pathToFileURL(join(options.directory,'client/dist/note-import.js')))
  const badImport=previewNotesCsv('body,priority,subject\n'+ 'x'.repeat(129)+',1,bob\nvalid,1.5,bob\nvalid,2,bob',{subject:'alice',maxErrors:1})
  assert.equal(badImport.ok,false);assert.equal(badImport.invalid,2);assert.equal(badImport.valid,1);assert.equal(badImport.truncated,true)
  assert.equal('rows' in badImport,false);assert.deepEqual(badImport.errors,[{row:2,code:'too_long',path:['body']}])
  const goodImport=previewNotesCsv('body,priority,subject\nvalid,2,bob',{subject:'alice'})
  assert.equal(goodImport.ok,true);assert.deepEqual(goodImport.rows,[{body:'valid',priority:2,subject:'alice'}])
  let calls=0,tokens=0,keys=0
  const client=notesClient({url:'https://notes.example',subject:'alice',accessToken:()=>{tokens++;return 'token'},fetch:async()=>{calls++;return new Response(JSON.stringify([{id:1,body:'valid',priority:0,version:1,subject:'alice'}]))}})
  const form=createNoteForm(client,{createKey:()=>{keys++;return '11111111-1111-4111-8111-111111111111'}})
  assert.deepEqual(await form.submit({body:'x'.repeat(129),tags:''}),{outcome:'validation_failed',fields:{body:'This note is too long.'}})
  assert.deepEqual([calls,tokens,keys],[0,0,0]);assert.equal(form.pending,null)
  assert.deepEqual(await form.submit({body:'valid',tags:''}),{outcome:'saved',note:{id:1,body:'valid',priority:0,version:1}})
  assert.deepEqual([calls,tokens,keys],[1,1,1])
  let guidedArtifact
  if(process.env.DATA_API_CHROMIUM_BIN){
   for(const name of ['main.ts','index.html'])await cp(new URL(`../../cmd/gregale/templates/data-api-starter/client/browser/${name}`,import.meta.url),join(options.directory,'client/browser',name))
   await execute('npm',['run','build:browser'],{cwd:join(options.directory,'client'),timeout:30000})
   await browserNoteEditor(options.directory)
   guidedArtifact=await browserDataBrowser(ready.inspector_url)
  }
  await close()
  if(guidedArtifact){
   const accessFile=join(options.directory,'data-api.access.json'),fixtureFile=join(options.directory,'guided-fixture.sql')
   await writeFile(fixtureFile,guidedArtifact.fixtureSQL)
   const verifyArgs=['--json','data-api','verify','--directory',options.directory,'--access-checks',accessFile,'--fixture',fixtureFile]
   const report=JSON.parse((await execute(options.cli,verifyArgs,{timeout:60000})).stdout)
   assert.equal(report.status,'passed');assert.equal(report.access_summary.passed,4);assert.equal(report.cleanup,'passed');assert.ok(!JSON.stringify(report).includes('inspector-browser-edited'))
   guidedArtifact.manifest.checks.find(check=>check.identity==='bob').steps[1].rows=1
   await writeFile(accessFile,JSON.stringify(guidedArtifact.manifest))
   await assert.rejects(execute(options.cli,verifyArgs,{timeout:60000}),error=>{const report=JSON.parse(error.stdout);assert.equal(report.status,'failed');assert.equal(report.cleanup,'passed');assert.equal(report.access_checks.find(check=>!check.passed).step,2);return true})
   guidedArtifact.manifest.checks.find(check=>check.identity==='bob').steps[1].rows=0
   const mutations=mutationScaffold(guidedArtifact.manifest);mutations.status='reviewed'
   mutations.mutationFixtures[0].keys={subject:'alice',body:'mutation-created-fixture'}
   const operation=(name,identity,method,fixture,body,status,rows,outcome)=>({name,identity,method,fixture,body,expect:{status,rows,outcome}})
   mutations.mutations=[
    operation('Bob_create_denied','bob','POST','created_row',{subject:'alice',body:'mutation-created-fixture'},403,0,'unchanged'),
    operation('Alice_create_allowed','alice','POST','created_row',{subject:'alice',body:'mutation-created-fixture'},201,1,'created'),
    operation('Bob_update_denied','bob','PATCH','step_1',{body:'mutation-not-allowed'},204,0,'unchanged'),
    operation('Alice_transfer_denied','alice','PATCH','step_1',{subject:'bob'},403,0,'unchanged'),
    operation('Alice_update_allowed','alice','PATCH','step_1',{body:'mutation-updated'},204,1,'updated'),
    operation('Bob_delete_denied','bob','DELETE','step_1',null,204,0,'unchanged'),
    operation('Alice_delete_allowed','alice','DELETE','step_1',null,204,1,'deleted')
   ]
   await writeFile(accessFile,JSON.stringify(mutations))
   const mutationReport=JSON.parse((await execute(options.cli,verifyArgs,{timeout:60000})).stdout)
   assert.equal(mutationReport.status,'passed');assert.equal(mutationReport.access_summary.passed,11);assert.equal(mutationReport.cleanup,'passed')
   assert.ok(mutationReport.access_checks.filter(check=>check.operation&&check.name.includes('denied')).every(check=>check.unchanged))
   assert.ok(!JSON.stringify(mutationReport).includes('mutation-updated'));assert.ok(!JSON.stringify(mutationReport).includes('mutation-created-fixture'))
   mutations.mutations[2].expect.status=403
   await writeFile(accessFile,JSON.stringify(mutations))
   await assert.rejects(execute(options.cli,verifyArgs,{timeout:60000}),error=>{const report=JSON.parse(error.stdout);assert.equal(report.status,'failed');assert.equal(report.cleanup,'passed');assert.equal(report.access_checks.find(check=>!check.passed&&!check.skipped).name,'Bob_update_denied');assert.ok(report.access_checks.at(-1).skipped);return true})
  }
  const args=['--json','data-api','dev','--directory',options.directory,'--port','0','--once','--validators','--check-types']
  await execute(options.cli,args,{timeout:90000})
  assert.equal(await readFile(options.output,'utf8'),generated)
  await writeFile(options.output,generated+'\n// stale validators\n')
  await assert.rejects(execute(options.cli,args,{timeout:90000}),error=>error.code!==0&&/stale/.test(error.stderr))
  assert.equal(await readFile(options.output,'utf8'),generated+'\n// stale validators\n')
  assert.deepEqual(await containers(),before);assert.deepEqual(await volumes(),beforeVolumes)
})

test('guided lifecycle checkpoints enforce downgrades and revocation with existing JWTs and preserve peers', {skip:!enabled,timeout:240000},async t=>{
 const before=await containers(),beforeVolumes=await volumes()
 const rolePredicate="EXISTS(SELECT 1 FROM api.lifecycle_memberships m WHERE m.subject=notes.subject AND m.access_level='editor')"
 const projectSetup=async project=>{
  await cp(new URL('../../cmd/gregale/templates/data-api/starter-verification.json',import.meta.url),join(project,'required-checks.json'))
  await writeFile(join(project,'data-api.dev.json'),JSON.stringify({version:1,identities:{alice:{subject:'alice',claims:{capability:'editor'}},bob:{subject:'bob',claims:{capability:'editor'}},admin:{subject:'admin'}},verify:{collection:'required-checks.json',scenarios:['notes-rls']},seed:'lifecycle-fixture.sql'}))
  await writeFile(join(project,'lifecycle-fixture.sql'),"INSERT INTO api.lifecycle_memberships(subject,access_level) VALUES ('alice','editor'),('bob','editor'); INSERT INTO api.notes(id,subject,body) OVERRIDING SYSTEM VALUE VALUES (1001,'alice','lifecycle-private-owner'),(1002,'bob','lifecycle-private-peer'); SELECT pg_catalog.setval('api.notes_id_seq',1002);")
  await writeFile(join(project,'migrations/sql/0017_lifecycle_memberships.sql'),`
CREATE TABLE api.lifecycle_memberships(subject text PRIMARY KEY,access_level text NOT NULL CHECK(access_level IN ('editor','reader')));
ALTER TABLE api.lifecycle_memberships ENABLE ROW LEVEL SECURITY;
ALTER TABLE api.lifecycle_memberships FORCE ROW LEVEL SECURITY;
CREATE POLICY lifecycle_admin ON api.lifecycle_memberships FOR ALL USING(current_setting('request.jwt.claims',true)::jsonb->>'sub'='admin') WITH CHECK(current_setting('request.jwt.claims',true)::jsonb->>'sub'='admin');
CREATE POLICY lifecycle_self ON api.lifecycle_memberships FOR SELECT USING(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub');
CREATE POLICY lifecycle_read ON api.notes AS RESTRICTIVE FOR SELECT USING(EXISTS(SELECT 1 FROM api.lifecycle_memberships m WHERE m.subject=notes.subject));
CREATE POLICY lifecycle_write ON api.notes AS RESTRICTIVE FOR UPDATE USING(${rolePredicate}) WITH CHECK(${rolePredicate});
`)
 }
 const {ready,options,close}=await runningFixture(t,{check:false,projectSetup})
 const link=new URL(ready.inspector_url),session=await(await fetch(new URL('session',link),{headers:{Authorization:'Bearer '+link.hash.slice(1)}})).json()
 const checkpoint=(name,identity,fixture,rows)=>({name,identity,fixture,method:'GET',body:null,expect:{status:200,rows,outcome:'visible'}})
 const write=(name,identity,fixture,method,body,outcome)=>({name,identity,fixture,method,body,expect:{status:204,rows:outcome==='unchanged'?0:1,outcome}})
 const manifest={version:5,status:'reviewed',view:{name:'Lifecycle notes',fingerprint:session.fingerprint,steps:[{schema:'api',table:'notes',columns:['id'],filter:null,sort:null,via:null}]},fixtures:[{name:'owned',schema:'api',table:'notes',keys:{id:1001,subject:'alice'}}],checks:[{identity:'alice',steps:[{fixture:'owned',rows:1}]},{identity:'bob',steps:[{fixture:'owned',rows:0}]}],mutationFixtures:[{name:'membership',schema:'api',table:'lifecycle_memberships',keys:{subject:'alice'}},{name:'peer',schema:'api',table:'notes',keys:{id:1002,subject:'bob'}}],mutations:[
 checkpoint('Member_before','alice','owned',1),write('Downgrade','admin','membership','PATCH',{access_level:'reader'},'updated'),checkpoint('Member_still_reads','alice','owned',1),write('Member_cannot_write','alice','owned','PATCH',{body:'lifecycle-unwanted-update'},'unchanged'),checkpoint('Peer_after_downgrade','bob','peer',1),write('Revoke','admin','membership','DELETE',null,'deleted'),checkpoint('Former_member_hidden','alice','owned',0),write('Former_member_cannot_write','alice','owned','PATCH',{body:'lifecycle-unwanted-update'},'unchanged'),checkpoint('Peer_after_revocation','bob','peer',1)
 ]}
 const accessFile=join(options.directory,'data-api.lifecycle.json');await writeFile(accessFile,JSON.stringify(manifest));await close()
 const args=['--json','data-api','verify','--directory',options.directory,'--access-checks',accessFile]
 const report=JSON.parse((await execute(options.cli,args,{timeout:60000})).stdout)
 assert.equal(report.status,'passed');assert.equal(report.access_summary.passed,11);assert.equal(report.cleanup,'passed')
 assert.ok(report.access_checks.filter(check=>check.checkpoint).every(check=>check.state_matches));assert.ok(!JSON.stringify(report).includes('lifecycle-private'))
 await writeFile(join(options.directory,'migrations/sql/0018_stale_role_policy.sql'),"DROP POLICY lifecycle_write ON api.notes; CREATE POLICY lifecycle_write ON api.notes AS RESTRICTIVE FOR UPDATE USING(current_setting('request.jwt.claims',true)::jsonb->>'capability'='editor') WITH CHECK(current_setting('request.jwt.claims',true)::jsonb->>'capability'='editor');")
 await assert.rejects(execute(options.cli,args,{timeout:60000}),error=>{const report=JSON.parse(error.stdout);assert.equal(report.status,'failed');assert.equal(report.cleanup,'passed');assert.equal(report.access_checks.find(check=>!check.passed&&!check.skipped).name,'Member_cannot_write');assert.ok(report.access_checks.at(-1).skipped);return true})
 await writeFile(join(options.directory,'migrations/sql/0019_stale_membership_policy.sql'),`DROP POLICY lifecycle_write ON api.notes; CREATE POLICY lifecycle_write ON api.notes AS RESTRICTIVE FOR UPDATE USING(${rolePredicate}) WITH CHECK(${rolePredicate}); DROP POLICY lifecycle_read ON api.notes; CREATE POLICY lifecycle_read ON api.notes AS RESTRICTIVE FOR SELECT USING(current_setting('request.jwt.claims',true)::jsonb->>'capability'='editor');`)
 await assert.rejects(execute(options.cli,args,{timeout:60000}),error=>{const report=JSON.parse(error.stdout);assert.equal(report.status,'failed');assert.equal(report.cleanup,'passed');assert.equal(report.access_checks.find(check=>!check.passed&&!check.skipped).name,'Former_member_hidden');assert.ok(report.access_checks.at(-1).skipped);assert.ok(!JSON.stringify(report).includes('lifecycle-private'));return true})
 assert.deepEqual(await containers(),before);assert.deepEqual(await volumes(),beforeVolumes)
})

test('read-only support views enforce column grants, tenant RLS and rejected writes',{skip:!enabled,timeout:180000},async t=>{
 const before=await containers(),beforeVolumes=await volumes()
 const setup=async project=>{
  await cp(new URL('../../cmd/gregale/templates/data-api-starter/migrations/examples/support/support.sql',import.meta.url),join(project,'migrations/sql/0017_support.sql'))
  await writeFile(join(project,'support-seed.sql'),"INSERT INTO support_private.cases(tenant,title,status,internal_notes) VALUES ('shop-a','Support case A','open','private A'),('shop-b','Support case B','open','private B'); INSERT INTO support_private.replies(tenant,case_id,body,internal_notes) SELECT tenant,id,'Support reply '||CASE tenant WHEN 'shop-a' THEN 'A' ELSE 'B' END,'private reply' FROM support_private.cases;")
  await writeFile(join(project,'data-api.dev.json'),JSON.stringify({version:1,identities:{alice:{subject:'alice'},bob:{subject:'bob'},support_a:{subject:'agent-a',read_only:true,claims:{tenant:'shop-a'}},support_b:{subject:'agent-b',read_only:true,claims:{tenant:'shop-b'}}},seed:'support-seed.sql',verify:{collection:'support-checks.json',scenarios:['support']}}))
  await writeFile(join(project,'support-checks.json'),JSON.stringify({version:2,scenarios:[{name:'support',requests:['support_a','support_b'].map((identity,index)=>({name:identity,identity,method:'GET',path:'/rest/v1/support_cases?select=title',expect:{status:200,json:[{title:'Support case '+(index===0?'A':'B')}]}}))}]}))
 }
 const {ready,close}=await runningFixture(t,{validators:true,projectSetup:setup})
 const {browserSupportViews}=await import('./browser-support-views.mjs');await browserSupportViews(ready.inspector_url)
 await close();assert.deepEqual(await containers(),before);assert.deepEqual(await volumes(),beforeVolumes)
})
