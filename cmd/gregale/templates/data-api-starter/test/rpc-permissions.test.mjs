import test from 'node:test'
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import { options } from '../migrations/rpc-permissions.mjs'

test('RPC permission setup previews by default and accepts an explicit role without SQL interpolation', () => {
  assert.deepEqual(options([]), { apply: false })
  assert.deepEqual(options(['--role', 'binding-role', '--apply']), { role: 'binding-role', apply: true })
  for (const args of [['--unknown'], ['--role'], ['--apply', '--apply'], ['--role', 'one', '--role', 'two']]) assert.throws(() => options(args))
})

test('permission setup CLI redacts migration connection failures', () => {
  const result = spawnSync(process.execPath, [fileURLToPath(new URL('../migrations/rpc-permissions.mjs', import.meta.url))], {
    env: { ...process.env, MIGRATION_DATABASE_URL: 'postgres://owner:permission-secret-sentinel@127.0.0.1:1/unavailable' }, encoding: 'utf8', timeout: 5000
  })
  assert.equal(result.status, 1)
  assert.match(result.stderr, /RPC permission setup failed/)
  assert.doesNotMatch(result.stdout + result.stderr, /permission-secret-sentinel|postgres:\/\//)
})
