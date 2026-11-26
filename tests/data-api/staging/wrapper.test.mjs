import test from 'node:test'
import assert from 'node:assert/strict'
import { copyFile, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { command } from './canary.mjs'

test('staging wrapper uses one source bundle and scrubs packaging subprocesses', async t => {
  const root = await mkdtemp(join(tmpdir(), 'gregale-wrapper-contract-'))
  t.after(() => rm(root, { recursive: true, force: true }))
  const bin = join(root, 'bin')
  await mkdir(bin); await mkdir(join(root, 'scripts/ci'), { recursive: true }); await mkdir(join(root, 'tmp'))
  const wrapper = join(root, 'scripts/ci/run-data-api-staging-canary.sh')
  await copyFile(fileURLToPath(new URL('../../../scripts/ci/run-data-api-staging-canary.sh', import.meta.url)), wrapper)
  const shim = async (name, body) => writeFile(join(bin, name), '#!/bin/sh\nset -e\n' + body + '\n', { mode: 0o755 })
  await shim('git', `if [ "$1" = rev-parse ]; then printf '%040d\\n' 0; fi`)
  await shim('go', `test -z "$FAAS_TOKEN"; test -z "$STAGING_CONFIG_JSON"; test "$UPDATE_DATA_API_RUNTIME_VERSION" = 0`)
  await shim('npm', `test -z "$FAAS_TOKEN"; test -z "$STAGING_CONFIG_JSON"; case "$1" in ci|install) printf '%s\\n' "$1" >> "$TMPDIR/npm-calls" ;; *) exit 1 ;; esac`)
  await shim('tar', `test -z "$FAAS_TOKEN"; while [ "$1" != -C ]; do shift; done; shift; touch "$1/gregale"; chmod +x "$1/gregale"`)
  await shim('node', `
if [ "$1" = tests/data-api/staging/canary.mjs ]; then
  test "$FAAS_TOKEN" = wrapper-private-token
  if [ "$2" != --check-config ]; then
    test -x "$GREGALE_CANARY_BIN"; test -f "$FAAS_DATA_API_SDK_BUNDLE"; test -f "$FAAS_DATA_API_SDK_TARBALL"
    printf 'verified wrapper\\n'
  fi
  exit 0
fi
test -z "$FAAS_TOKEN"; test -z "$STAGING_CONFIG_JSON"
if [ "$1" = -p ]; then printf '0123456789abcdef\\n'; exit 0; fi
if [ "$1" = scripts/build-data-api-bundle.mjs ]; then
  while [ "$1" != --out-dir ]; do shift; done; shift; mkdir -p "$1"
  printf '%s' '{"cli":{"archives":[{"file":"gregale_0.0.0-staging.0_linux_amd64.tar.gz"}]},"sdk":{"file":"gregale-data-0.1.1.tgz"}}' > "$1/data-api-bundle.json"
  touch "$1/gregale_0.0.0-staging.0_linux_amd64.tar.gz" "$1/gregale-data-0.1.1.tgz"
  exit 0
fi
exec "$REAL_NODE" "$@"`)
  const env = { ...process.env, PATH: `${bin}:${process.env.PATH}`, REAL_NODE: process.execPath, TMPDIR: join(root, 'tmp'), FAAS_TOKEN: 'wrapper-private-token', STAGING_CONFIG_JSON: 'wrapper-private-config', FAAS_DATA_API_STAGING_CONFIG: join(root, 'config.json') }
  const output = await command('bash', [wrapper], { cwd: root, env, timeout: 15000 })
  assert.match(output, /verified wrapper/)
  assert.doesNotMatch(output, /wrapper-private-token|wrapper-private-config/)
  assert.equal(await readFile(join(root, 'tmp/npm-calls'), 'utf8'), 'ci\ninstall\n')
  assert.deepEqual(await readFile(join(root, 'scripts/ci/run-data-api-staging-canary.sh'), 'utf8'), await readFile(fileURLToPath(new URL('../../../scripts/ci/run-data-api-staging-canary.sh', import.meta.url)), 'utf8'))
})
