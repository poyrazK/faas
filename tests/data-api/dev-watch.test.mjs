import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, writeFile, rename, rm, mkdir } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { setTimeout as delay } from 'node:timers/promises'

const runtime = process.env.DATA_API_RUNTIME_DIR
const dev = runtime ? await import(pathToFileURL(join(runtime, 'dev.mjs'))) : undefined

async function until(condition) {
  for (let i = 0; i < 100; i++) { if (condition()) return; await delay(50) }
  assert.fail('Watcher did not observe the expected change')
}

test('migration watching serializes reloads and notices an atomic save during a reload', { skip: !runtime, timeout: 10000 }, async t => {
  const directory = await mkdtemp(join(tmpdir(), 'gregale-watch-'))
  const path = join(directory, '0001_notes.sql')
  await writeFile(path, 'SELECT 1;')
  const controller = new AbortController()
  let calls = 0, active = 0, maximum = 0, release
  const held = new Promise(resolve => { release = resolve })
  const initial = await dev.migrationSource(directory)
  const watching = dev.watchMigrations({ directory, initial, signal: controller.signal,
    report: () => assert.fail('Unexpected reload error'),
    reload: async () => { calls++; active++; maximum = Math.max(maximum, active); if (calls === 1) await held; active-- }
  })
  t.after(async () => { controller.abort(); release(); await watching; await rm(directory, { recursive: true, force: true }) })
  await writeFile(path, 'SELECT 2;')
  await until(() => calls === 1)
  const replacement = join(directory, 'replacement.tmp')
  await writeFile(replacement, 'SELECT 3;')
  await rename(replacement, path)
  release()
  await until(() => calls === 2)
  await delay(600)
  assert.equal(calls, 2)
  assert.equal(maximum, 1)
})

test('a failed reload retries after the migration source is repaired without a busy loop', { skip: !runtime, timeout: 10000 }, async t => {
  const directory = await mkdtemp(join(tmpdir(), 'gregale-watch-failure-'))
  const path = join(directory, '0001_notes.sql')
  await writeFile(path, 'SELECT 1;')
  const initial = await dev.migrationSource(directory)
  const controller = new AbortController()
  let calls = 0, failures = 0
  const watching = dev.watchMigrations({ directory, initial, signal: controller.signal,
    report: status => { assert.equal(status, 'reload_failed'); failures++ },
    reload: () => { calls++; if (calls === 1) throw new Error('Invalid SQL') }
  })
  t.after(async () => { controller.abort(); await watching; await rm(directory, { recursive: true, force: true }) })
  await writeFile(path, 'INVALID SQL;')
  await until(() => failures === 1)
  await delay(750)
  assert.equal(calls, 1)
  await writeFile(path, 'SELECT 1;')
  await until(() => calls === 2)
  assert.equal(failures, 1)
})

test('watching recovers when the migrations directory is replaced with identical contents', { skip: !runtime, timeout: 10000 }, async t => {
  const directory = await mkdtemp(join(tmpdir(), 'gregale-watch-directory-'))
  const path = join(directory, '0001_notes.sql')
  await writeFile(path, 'SELECT 1;')
  const initial = await dev.migrationSource(directory)
  const controller = new AbortController()
  let calls = 0, failures = 0
  const watching = dev.watchMigrations({ directory, initial, signal: controller.signal,
    reload: () => { calls++ }, report: status => { assert.equal(status, 'source_unavailable'); failures++ }
  })
  t.after(async () => { controller.abort(); await watching; await rm(directory, { recursive: true, force: true }) })
  await rm(directory, { recursive: true })
  await until(() => failures === 1)
  await delay(600)
  assert.equal(failures, 1)
  await mkdir(directory)
  await writeFile(path, 'SELECT 1;')
  await until(() => calls === 1)
})

test('development output selects validators without changing plain output or schema fingerprints', {skip:!runtime}, async()=>{
  const {generate,fingerprint}=await import(pathToFileURL(join(runtime,'types.mjs')))
  const snapshot={version:3,schemas:['api'],tables:[{schema:'api',name:'notes',view:false,relationships:[],columns:[{name:'body',type:'string',nullable:false,optional:false,insertable:true,updatable:true}]}],functions:[],enums:{},composites:[]}
  const metadata=[{schema:'api',relation:'notes',name:'body',type:'varchar',typmod:12}]
  assert.equal(dev.developmentTypes(snapshot,metadata,false),generate(snapshot))
  const withValidators=dev.developmentTypes(snapshot,metadata,true)
  assert.ok(withValidators.startsWith(generate(snapshot)))
  assert.ok(withValidators.includes('export const inputValidators'))
  assert.ok(withValidators.includes('"maxLength":8'))
  assert.ok(withValidators.includes(`// Schema fingerprint: ${fingerprint(snapshot)}`))
})
