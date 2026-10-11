import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { createDataClient } from '../../../sdk/data/dist/index.js'
import { qualifyRollout } from './rollout.mjs'
import { CanaryFailure } from './checks.mjs'

for (const failure of ['', 'committed', 'stale-ready', 'missing-diagnostic']) test(`hosted rollout ${failure || 'success'} exercises SDK guards and recovery`, async t => {
  const dir = await mkdtemp(join(tmpdir(), 'hosted-rollout-'))
  t.after(() => rm(dir, { recursive: true, force: true }))
  const older = 'a'.repeat(64), current = 'b'.repeat(64)
  const olderPath = join(dir, 'older.json'), currentPath = join(dir, 'current.json')
  await writeFile(olderPath, JSON.stringify({ fingerprint: older }))
  await writeFile(currentPath, JSON.stringify({ fingerprint: current }))
  let policy, rows = [], next = 1
  const checks = [], methods = []
  const runner = {
    workspace: dir, prefix: 'unique-run', config: { api: 'https://management.example', token: 'secret' }, cli: 'gregale', report: { deployments: [{ app: 'owned-api', deployment_id: 'deployed-version' }] },
    wait: async (read, ready) => { const value = await read(); if (!ready(value)) throw new CanaryFailure('operation_timeout'); return value },
    save: async () => {}, check: async (name, action) => { await action(); checks.push(name) }, contract: async () => {},
    exec: async (args, _cwd, stdin) => {
      if (args[1] === 'diff') await writeFile(args.at(-1), JSON.stringify({ version: 1, current_fingerprint: current, accepted_fingerprints: [older] }))
      if (args[0] === 'secrets') policy = args[1] === 'unset' ? undefined : JSON.parse(stdin.trim().split('=').slice(1).join('='))
    },
    refresh: async () => { if (policy?.current_fingerprint === older && failure !== 'stale-ready') throw new CanaryFailure('command_failed') },
    runCommand: async () => failure === 'missing-diagnostic' ? '' : JSON.stringify({ deployment_id: 'deployed-version', line: 'Error: Accepted contracts do not match the serving contract' }),
    publicFetch: async (input, init = {}) => {
      const url = new URL(input), method = init.method || 'GET'
      methods.push(method)
      if (policy?.current_fingerprint === older) return new Response('{}', { status: 503 })
      const pin = /\/__gregale\/contract\/([a-f0-9]{64})\//.exec(url.pathname)?.[1]
      assert.ok(pin || url.pathname === '/rest/v1/notes')
      if (pin && pin !== current && !policy?.accepted_fingerprints.includes(pin)) return new Response('{"code":"schema_contract_mismatch"}', { status: 409 })
      const body = init.body ? JSON.parse(init.body) : null
      if (method === 'POST') { const row = { id: next++, body: body.body || body.note_body }; rows.push(row); return new Response(JSON.stringify(new Headers(init.headers).get('accept')?.includes('object') ? row : [row]), { status: 201 }) }
      if (method === 'PATCH') { const row = rows.find(r => 'eq.'+r.id === url.searchParams.get('id')); Object.assign(row, body); return new Response(JSON.stringify(row)) }
      if (method === 'DELETE') { const deleted = rows.filter(r => 'eq.'+r.id === url.searchParams.get('id')); rows = rows.filter(r => !deleted.includes(r)); return new Response(JSON.stringify(deleted)) }
      const found = failure === 'committed' ? [{ id: 99 }] : rows.filter(r => 'eq.'+r.body === url.searchParams.get('body'))
      return new Response(JSON.stringify(found))
    },
  }
  const action = qualifyRollout(runner, { app: { slug: 'owned-api' }, url: 'https://app.example', token: () => 'application-token', createDataClient, olderPath, currentPath, clientDir: dir })
  if (failure) await assert.rejects(action, { code: { committed: 'rollout_rejected_write_committed', 'stale-ready': 'rollout_stale_policy_ready', 'missing-diagnostic': 'operation_timeout' }[failure] })
  else { await action; assert.equal(checks.length, 4); assert.equal(runner.report.contract_rollout.stale_policy_rejected, true); assert.ok(methods.includes('PATCH')); assert.ok(methods.includes('DELETE')) }
  assert.equal(policy, undefined, 'serving policy restored after success and failure')
})
