import test from 'node:test'
import assert from 'node:assert/strict'
import { EventEmitter } from 'node:events'
import { access, mkdtemp, readFile, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { verifyBrowserCORS } from './browser-cors.mjs'

test('browser startup failure reports diagnostics and cleans child processes, profile and listeners', async t => {
  const root = await mkdtemp(join(tmpdir(), 'gregale-browser-failure-'))
  const binary = join(root, 'browser')
  await writeFile(binary, `#!/bin/sh
for arg in "$@"; do
  case "$arg" in --user-data-dir=*) profile="\${arg#--user-data-dir=}" ;; esac
done
printf '%s' "$profile" > '${join(root, 'profile-path')}'
# Keep stderr open and write the profile after the parent exits, like a
# Chromium utility process. Cleanup must terminate this child as well.
(while :; do mkdir -p "$profile/Default"; printf 'writing' > "$profile/Default/state"; sleep 0.01; done) &
printf 'fixture_browser_startup_failure\\n' >&2
exit 42
`, { mode: 0o700 })
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
  const profile = await readFile(join(root, 'profile-path'), 'utf8')
  await assert.rejects(access(profile), { code: 'ENOENT' })
})
