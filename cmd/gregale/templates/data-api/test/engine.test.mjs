import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import { chmodSync, mkdtempSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { createEngine } from '../engine.mjs'
import { runtimeConfig } from '../config.mjs'
import { createServer } from '../server.mjs'

// fakeEngine answers /ready with 200 after FAKE_READY_AFTER_MS on
// FAKE_READY_PORT, like PostgREST's admin listener once its pool is up.
function fakeEngine() {
  const file = join(mkdtempSync(join(tmpdir(), 'data-api-engine-')), 'engine')
  writeFileSync(file, `#!${process.execPath}
const http = require('node:http')
const started = Date.now()
http.createServer((req, res) => { res.writeHead(Date.now() - started >= Number(process.env.FAKE_READY_AFTER_MS) ? 200 : 503); res.end() })
  .listen(Number(process.env.FAKE_READY_PORT), '127.0.0.1')
if (process.env.FAKE_EXIT_AFTER_MS) setTimeout(() => process.exit(3), Number(process.env.FAKE_EXIT_AFTER_MS))
`)
  chmodSync(file, 0o755)
  return file
}

async function freePort() {
  const server = net.createServer()
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const { port } = server.address()
  await new Promise(resolve => server.close(resolve))
  return port
}

const listening = port => new Promise(resolve => {
  const socket = net.connect(port, '127.0.0.1', () => { socket.destroy(); resolve(true) })
  socket.on('error', () => resolve(false))
})

test('the engine stops for a checkpoint, starts again after a restore and is awaited until ready', { timeout: 20000 }, async t => {
  const readyPort = await freePort(), exits = []
  const engine = createEngine({ bin: fakeEngine(), env: { ...process.env, FAKE_READY_PORT: String(readyPort), FAKE_READY_AFTER_MS: '300' }, readyPort, onExit: exit => exits.push(exit) })
  t.after(() => engine.stop())
  engine.start()
  assert.equal(await engine.ready(50), false, 'a deadline shorter than the engine startup ends the wait')
  assert.equal(await engine.ready(5000), true)
  assert.equal(await engine.ready(0), true, 'a ready engine answers without waiting')

  await engine.stop()
  assert.equal(await listening(readyPort), false, 'no engine process survives the checkpoint')
  engine.start()
  assert.equal(await engine.ready(5000), true, 'a fresh engine after the restore')
  await engine.stop()
  // A VM that keeps running after a checkpoint gets its engine back on demand.
  assert.equal(await engine.ready(5000), true)
  assert.deepEqual(exits, [], 'stopping is not an engine failure')
})

test('an engine that exits or cannot start on its own is reported', { timeout: 20000 }, async () => {
  const readyPort = await freePort()
  const reported = () => {
    let onExit
    const exit = new Promise(resolve => { onExit = resolve })
    return { exit, onExit }
  }
  const crash = reported()
  createEngine({ bin: fakeEngine(), env: { ...process.env, FAKE_READY_PORT: String(readyPort), FAKE_READY_AFTER_MS: '0', FAKE_EXIT_AFTER_MS: '200' }, readyPort, onExit: crash.onExit }).start()
  assert.deepEqual(await crash.exit, { code: 3 })
  const missing = reported()
  createEngine({ bin: '/nonexistent/postgrest', env: process.env, readyPort, onExit: missing.onExit }).start()
  assert.equal((await missing.exit).error.code, 'ENOENT')
})

test('lifecycle routes need guest-init\'s header and requests wait for the engine', async t => {
  const config = runtimeConfig({ DATABASE_URL: 'postgres://u:p@127.0.0.1/db', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes' })
  const calls = []
  let engineReady = false
  config.lifecycle = {
    ready: async waitMs => { calls.push(['ready', waitMs > 0]); return engineReady },
    beforeCheckpoint: async () => { calls.push(['before']) },
    afterRestore: () => { calls.push(['after']) },
  }
  const upstream = (await import('node:http')).createServer((_req, res) => res.end('[]'))
  await new Promise(resolve => upstream.listen(0, '127.0.0.1', resolve))
  const server = createServer(config, async () => ({ sub: 'alice', exp: 9999999999 }), upstream.address().port, undefined, () => {})
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  t.after(() => { server.close(); upstream.close() })
  const url = `http://127.0.0.1:${server.address().port}`
  for (const [path, header] of [['/__gregale/lifecycle/before-checkpoint', 'x-faas-before-checkpoint'], ['/__gregale/lifecycle/after-restore', 'x-faas-after-restore']]) {
    assert.equal((await fetch(url + path, { method: 'POST' })).status, 404, 'a public request cannot trigger a lifecycle step')
    assert.equal((await fetch(url + path, { headers: { [header]: '1' } })).status, 404)
    assert.equal((await fetch(url + path, { method: 'POST', headers: { [header]: '1' } })).status, 204)
  }
  assert.deepEqual(calls, [['before'], ['after']])
  const unavailable = await fetch(url + '/rest/v1/notes', { headers: { Authorization: 'Bearer fixture' } })
  assert.deepEqual([unavailable.status, (await unavailable.json()).code], [503, 'data_api_unavailable'])
  engineReady = true
  assert.equal((await fetch(url + '/rest/v1/notes', { headers: { Authorization: 'Bearer fixture' } })).status, 200)
  assert.deepEqual(calls.slice(2), [['ready', true], ['ready', true]], 'requests wait within their deadline')
})
