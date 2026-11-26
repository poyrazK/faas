import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const task = readFileSync(fileURLToPath(new URL('../../cmd/gregale/data_api_write_inspect_task.js', import.meta.url)), 'utf8')

test('hosted inspection reports transport without credential or certificate details', t => {
  const dir = mkdtempSync(join(tmpdir(), 'gregale-transport-'))
  t.after(() => rmSync(dir, { recursive: true, force: true }))
  mkdirSync(join(dir, 'node_modules/pg'), { recursive: true })
  writeFileSync(join(dir, 'node_modules/pg/index.js'), `
    class Client {
      constructor() { this.connection = { stream: { write() {}, encrypted: process.env.ENCRYPTED === 'true', authorized: process.env.AUTHORIZED === 'true' } } }
      async connect() {}
      async end() {}
      async query(sql) {
        if (sql.includes('FROM pg_roles')) return {rows:[{name:'app',database:'db',superuser:false,bypass_rls:false}]}
        if (sql.includes('pg_has_role(current_user,c.relowner')) return {rows:[{name:'notes',kind:'r'}]}
        if (sql.includes('FROM pg_attribute')) return {rows:[{name:'id'}]}
        return {rows:[]}
      }
    }
    module.exports = { Client }
  `)
  for (const [encrypted, authorized, expected] of [
    ['false', 'false', { encrypted: false, peer_authorized: null }],
    ['true', 'true', { encrypted: true, peer_authorized: true }],
    ['true', 'false', { encrypted: true, peer_authorized: false }],
  ]) {
    const output = execFileSync(process.execPath, ['-e', task, 'notes'], {
      cwd: dir,
      env: { ...process.env, DATABASE_URL: 'postgres://secret-user:secret-password@secret-host/db', ENCRYPTED: encrypted, AUTHORIZED: authorized },
      encoding: 'utf8',
    })
    assert.deepEqual(JSON.parse(output).transport, expected)
    assert.doesNotMatch(output, /secret-user|secret-password|secret-host/)
  }
})

test('hosted inspection task fits the app-task command limit', () => {
  assert.ok(Buffer.byteLength(task) <= 4096)
})
