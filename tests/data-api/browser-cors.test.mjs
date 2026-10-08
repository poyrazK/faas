import test from 'node:test'
import assert from 'node:assert/strict'
import { EventEmitter } from 'node:events'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { verifyBrowserCORS } from './browser-cors.mjs'

test('browser startup failure reports bounded diagnostics and cleans request listeners', async t => {
  const root = await mkdtemp(join(tmpdir(), 'gregale-browser-failure-'))
  const binary = join(root, 'browser')
  await writeFile(binary, '#!/bin/sh\nprintf "fixture_browser_startup_failure\\n" >&2\nexit 42\n', { mode: 0o700 })
  const previous = process.env.DATA_API_CHROMIUM_BIN
  process.env.DATA_API_CHROMIUM_BIN = binary
  t.after(async () => {
    if (previous === undefined) delete process.env.DATA_API_CHROMIUM_BIN
    else process.env.DATA_API_CHROMIUM_BIN = previous
    await rm(root, { recursive: true, force: true })
  })
  const gateway = new EventEmitter()
  await assert.rejects(verifyBrowserCORS({ gateway }), /chromium_not_ready \(exit=42, signal=null\): fixture_browser_startup_failure/)
  assert.equal(gateway.listenerCount('request'), 0)
})
