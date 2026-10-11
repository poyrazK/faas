import { readFile } from 'node:fs/promises'
import { join } from 'node:path'
import { requireValue, CanaryFailure } from './checks.mjs'

export async function captureContract(runner, app, clientDir, name) {
  const path = join(clientDir, `${name}.schema.json`)
  await runner.exec(['data-api', 'types', app.slug, '--snapshot', '--output', path])
  const contract = JSON.parse(await readFile(path, 'utf8'))
  requireValue(contract.fingerprint === runner.report.syncs.at(-1).fingerprint, 'rollout_snapshot_mismatch')
  return path
}

export async function qualifyRollout(runner, { app, url, token, createDataClient, olderPath, currentPath, clientDir }) {
  const older = JSON.parse(await readFile(olderPath, 'utf8')).fingerprint
  const current = JSON.parse(await readFile(currentPath, 'utf8')).fingerprint
  requireValue(older !== current, 'rollout_contract_unchanged')
  const policyPath = join(clientDir, 'accepted-contracts.json')
  await runner.exec(['data-api', 'diff', '--baseline', olderPath, '--current', currentPath, '--accept-contract', policyPath])
  const policy = JSON.parse(await readFile(policyPath, 'utf8'))
  requireValue(policy.version === 1 && policy.current_fingerprint === current && policy.accepted_fingerprints?.length === 1 && policy.accepted_fingerprints[0] === older, 'rollout_policy_invalid')
  const client = fingerprint => createDataClient({ url, accessToken: token, expectedSchemaFingerprint: fingerprint, fetch: (input, init) => runner.publicFetch(input, init) }).schema('api')
  const old = client(older), fresh = client(current)
  const bad = client((current[0] === 'a' ? 'b' : 'a') + current.slice(1))
  const rejected = result => requireValue(result.status === 409 && result.error?.code === 'schema_contract_mismatch' && result.data === null, 'rollout_rejection_missing')
  const install = async value => {
    await runner.exec(['secrets', 'set', '--app', app.slug, '--from-stdin'], runner.workspace, `DATA_API_ACCEPTED_CONTRACTS=${JSON.stringify(value)}\n`)
    await runner.refresh(app)
    await runner.contract(url, token(), current)
  }
  try {
    await runner.check('hosted_compatible_contract_acceptance', async () => {
      await install(policy)
      const inserted = await old.from('notes').insert({ subject: 'alice', body: runner.prefix }).select('id').single().retry(false)
      requireValue(!inserted.error && inserted.data?.id, 'rollout_old_insert_failed')
      const rpc = await old.rpc('create_note', { note_body: runner.prefix }).retry(false)
      requireValue(!rpc.error && rpc.data?.length === 1, 'rollout_old_rpc_failed')
      const updated = await fresh.from('notes').update({ body: runner.prefix + '-updated' }).eq('id', inserted.data.id).select('id').single().retry(false)
      requireValue(!updated.error, 'rollout_current_update_failed')
      const deleted = await old.from('notes').delete().eq('id', inserted.data.id).select('id').retry(false)
      requireValue(!deleted.error && deleted.data?.length === 1, 'rollout_old_delete_failed')
    })
    await runner.check('hosted_unapproved_write_and_rpc_rejection', async () => {
      const marker = runner.prefix + '-forbidden'
      rejected(await bad.from('notes').insert({ subject: 'alice', body: marker }).retry(false))
      rejected(await bad.rpc('create_note', { note_body: marker }).retry(false))
      const rows = await fresh.from('notes').select('id').eq('body', marker).retry(false)
      requireValue(!rows.error && rows.data?.length === 0, 'rollout_rejected_write_committed')
    })
    await runner.check('hosted_contract_revocation', async () => {
      await runner.exec(['secrets', 'unset', '--app', app.slug, 'DATA_API_ACCEPTED_CONTRACTS'])
      await runner.refresh(app)
      await runner.contract(url, token(), current)
      rejected(await old.from('notes').select('id').retry(false))
      requireValue(!(await fresh.from('notes').select('id').retry(false)).error, 'rollout_current_revoked')
    })
    await runner.check('hosted_stale_policy_fail_closed', async () => {
      // Bind a reviewed policy to the previous schema. The runtime must refuse
      // readiness; a successful refresh would incorrectly attest acceptance.
      await runner.exec(['secrets', 'set', '--app', app.slug, '--from-stdin'], runner.workspace, `DATA_API_ACCEPTED_CONTRACTS=${JSON.stringify({ ...policy, current_fingerprint: older, accepted_fingerprints: [current] })}\n`)
      let failed = false
      try { await runner.refresh(app) } catch (error) { if (!(error instanceof CanaryFailure)) throw error; failed = true }
      requireValue(failed, 'rollout_stale_policy_ready')
      const response = await runner.publicFetch(url + '/rest/v1/notes', { headers: { Authorization: `Bearer ${token()}` } })
      requireValue([502, 503, 504].includes(response.status), 'rollout_stale_policy_served')
      await runner.wait(async () => {
        const output = await runner.runCommand(runner.cli, ['--json', 'logs', app.slug, '--source', 'runtime', '--grep', 'Accepted contracts do not match the serving contract'], { cwd: runner.workspace, env: { ...process.env, FAAS_API: runner.config.api, FAAS_TOKEN: runner.config.token }, timeout: 30_000, signal: runner.signal })
        const deployment = runner.report.deployments.find(x => x.app === app.slug)?.deployment_id
        return output.split('\n').some(line => { try { const record = JSON.parse(line); return record.deployment_id === deployment && typeof record.line === 'string' && record.line.includes('Accepted contracts do not match the serving contract') } catch { return false } })
      }, found => found === true, 30_000)
    })
    runner.report.contract_rollout = { older, current, accepted: true, revoked: true, stale_policy_rejected: true }
    await runner.save()
  } finally {
    // Restore a healthy serving app even when a rollout assertion fails.
    await runner.exec(['secrets', 'unset', '--app', app.slug, 'DATA_API_ACCEPTED_CONTRACTS'])
    await runner.refresh(app)
    await runner.contract(url, token(), current)
  }
}
