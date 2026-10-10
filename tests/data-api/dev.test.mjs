import test from 'node:test'
import http from 'node:http'
import assert from 'node:assert/strict'
import { spawn, execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { mkdtemp, cp, writeFile, readFile, readdir, stat, rm, unlink } from 'node:fs/promises'
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

async function fixture(t, overrides = {}) {
  const directory = await mkdtemp(join(tmpdir(), 'gregale-dev-test-'))
  t.after(() => rm(directory, { recursive: true, force: true }))
  const project = join(directory, 'project')
  await cp(starter, project, { recursive: true })
  if (!overrides.starterReplay) await rm(join(project, 'data-api.requests.json'), { force: true })
  const { starterReplay, ...runtimeOverrides } = overrides
  const options = { directory: project, output: join(project, 'client/src/database.types.ts'), port: 0, once: true, check: true, cli: process.env.DATA_API_DEV_CLI, ...runtimeOverrides }
  const config = join(directory, 'options.json')
  await writeFile(config, JSON.stringify(options))
  return { config, options }
}

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
  await writeFile(join(options.directory, 'migrations/sql/0011_invalid.sql'), 'INVALID SQL;')
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
  const path = join(directory, '0011_local_reload.sql')
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
  const pendingMigration = join(directory, '0012_pending.sql')
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
  await writeFile(join(directory, '0013_unsafe.sql'), 'ALTER TABLE api.notes DISABLE ROW LEVEL SECURITY;')
  assert.match((await wait('reload_failed', mark)).detail, /RLS and RPC permission checks/)
  assert.equal((await request('/rest/v1/notes')).status, 503)
  mark = events.length
  await writeFile(join(directory, '0014_repair_and_rpc.sql'), `ALTER TABLE api.notes ENABLE ROW LEVEL SECURITY;
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
  await writeFile(join(directory, '0015_client_check.sql'), 'ALTER TABLE api.notes ADD COLUMN checked_label text;')
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
  await writeFile(join(directory, '0016_client_repaired.sql'), 'SELECT 1;')
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
  await writeFile(join(directory, '0011_compatible.sql'), 'ALTER TABLE api.notes ADD COLUMN local_label text;')
  const compatible = await wait('compatibility', mark)
  assert.equal(compatible.breaking, 0)
  assert.ok(compatible.changes.some(change => change.path === 'api.notes.local_label' && !change.breaking))
  await wait('reloaded', mark)
  const lastGood = JSON.parse(await readFile(baseline, 'utf8'))
  mark = events.length
  await writeFile(join(directory, '0012_breaking.sql'), 'ALTER TABLE api.notes DROP COLUMN local_label; DROP FUNCTION api.create_note_with_tags(text,text[]);')
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
  await writeFile(join(directory, '0011_compatible.sql'), 'ALTER TABLE api.notes ADD COLUMN local_label text;')
  assert.equal((await wait('compatibility', mark)).breaking, 0)
  await wait('reloaded', mark)
  const accepted = await readFile(baseline, 'utf8'), types = await readFile(options.output, 'utf8')
  mark = events.length
  await writeFile(join(directory, '0012_breaking.sql'), 'ALTER TABLE api.notes DROP COLUMN priority;')
  const change = await wait('compatibility', mark)
  assert.ok(change.changes.some(change => change.path === 'api.notes.priority' && change.breaking))
  assert.match((await wait('reload_failed', mark)).detail, /Breaking schema changes rejected/)
  assert.equal((await fetch(`${ready.url}/healthz`)).status, 503)
  assert.equal(await readFile(options.output, 'utf8'), types)
  assert.equal(await readFile(baseline, 'utf8'), accepted)
  mark = events.length
  await writeFile(join(directory, '0013_repair.sql'), 'ALTER TABLE api.notes ADD COLUMN priority integer NOT NULL DEFAULT 0;')
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
  await writeFile(join(directory, '0014_break_again.sql'), 'ALTER TABLE api.notes DROP COLUMN priority;')
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
      await writeFile(join(local.options.directory, 'migrations/sql/0099_inspector.sql'), 'THIS IS NOT SQL;\n')
      await local.wait('reload_failed', start)
      await wait("document.getElementById('status').textContent.includes('paused') && document.getElementById('detail').textContent.includes('SQLSTATE')")
      await writeFile(join(local.options.directory, 'migrations/sql/0099_inspector.sql'), 'ALTER TABLE api.notes ADD COLUMN inspector_example text;\n')
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
