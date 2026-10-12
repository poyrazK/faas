import assert from 'node:assert/strict'
import { releaseExamples } from './data-api-bundle-examples.mjs'
import { createHash, randomBytes } from 'node:crypto'
import { readFile, writeFile, rename, rm } from 'node:fs/promises'
import { join } from 'node:path'
import { validateBundle, readJSON, verifyFile, hostArchive } from '../cmd/gregale/templates/data-api-starter/tools/artifact-lib.mjs'

export const releaseChecks = Object.freeze([
  'fresh_starter_install', 'pin_restore', 'client_types_and_browser_build',
  'starter_scenario_replay', 'generic_install_restore',
  'compatible_policy_generation', 'packaged_sdk_mixed_clients_and_rls',
  'unapproved_write_and_rpc_rejection', 'pinned_scenario_replay',
  'policy_revocation', 'stale_policy_rejection', 'local_session_cleanup',
  'custom_project_install_restore', 'custom_project_types', 'custom_project_crud_rpc_and_rls',
  'custom_project_contract_rollouts', 'packaged_sdk_features',
  ...releaseExamples.map(name=>`packaged_example_${name}`),
])
const sha256 = data => createHash('sha256').update(data).digest('hex')
export const reportName = 'data-api-verification.json'
export const reportSumsName = 'DATA-API-VERIFICATION-SHA256SUMS'

export async function clearReleaseReport(directory) {
  await rm(join(directory, reportName), { force: true })
  await rm(join(directory, reportSumsName), { force: true })
}

export async function bundleEvidence(directory, expectedBundle) {
  const bundle = validateBundle(await readJSON(join(directory, 'data-api-bundle.json')))
  if (expectedBundle) assert.deepEqual(bundle, expectedBundle, 'Bundle metadata changed during verification')
  const entries = [...bundle.cli.archives, bundle.sdk]
  for (const entry of entries) await verifyFile(join(directory, entry.file), entry)
  const manifestHash = sha256(await readFile(join(directory, 'data-api-bundle.json')))
  const files = [...entries.map(entry => ({file:entry.file,sha256:entry.sha256})), {file:'data-api-bundle.json',sha256:manifestHash}]
  // Use the builder's code-point filename order, independently of the locale.
  files.sort((a,b)=>a.file < b.file ? -1 : a.file > b.file ? 1 : 0)
  const sums = await readFile(join(directory, 'DATA-API-SHA256SUMS'))
  assert.equal(sums.toString('utf8'),files.map(entry=>`${entry.sha256}  ${entry.file}\n`).join(''),'Bundle checksum list must cover the exact manifest and archives')
  return { source_commit:bundle.source.commit, manifest_sha256:manifestHash, checksums_sha256:sha256(sums), executed_target:`${hostArchive(bundle).os}/${hostArchive(bundle).arch}`, artifacts:files }
}

export async function writeReleaseReport(directory, bundle, checks, contracts, customContracts, examples) {
  assert.deepEqual([...checks].sort(), [...releaseChecks].sort(), 'Every release check must pass before writing a report')
  for (const key of ['older','current']) assert.match(contracts[key],/^[a-f0-9]{64}$/)
  for (const key of ['older','current']) assert.match(customContracts?.[key] ?? '',/^[a-f0-9]{64}$/,'Custom-project contract evidence is required')
  assert.deepEqual(Object.keys(examples ?? {}).sort(), [...releaseExamples].sort(), 'Every packaged example requires evidence')
  const exampleContracts={}
  for (const name of releaseExamples) { assert.match(examples[name],/^[a-f0-9]{64}$/); exampleContracts[name]=examples[name] }
  const evidence = await bundleEvidence(directory,bundle)
  const report = {format:1,status:'passed',scope:'local_packaged_data_api',...evidence,contracts:{older:contracts.older,current:contracts.current},custom_contracts:{older:customContracts.older,current:customContracts.current},examples:exampleContracts,checks:releaseChecks.map(name=>({name,status:'passed'}))}
  const bytes = `${JSON.stringify(report,null,2)}\n`
  const temporary = join(directory,`.data-api-verification-${randomBytes(12).toString('hex')}`)
  try {
    await writeFile(temporary,bytes,{flag:'wx',mode:0o644})
    await rename(temporary,join(directory,reportName))
    await writeFile(join(directory,reportSumsName),`${sha256(bytes)}  ${reportName}\n`,{flag:'wx',mode:0o644})
  } catch (error) { await clearReleaseReport(directory); throw error }
  finally { await rm(temporary,{force:true}) }
  return report
}
