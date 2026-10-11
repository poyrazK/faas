import test from 'node:test'
import { releaseExamples } from './data-api-bundle-examples.mjs'
const examples=Object.fromEntries(releaseExamples.map(name=>[name,'e'.repeat(64)]))
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { mkdtemp, writeFile, readFile, rm, access } from 'node:fs/promises'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { bundleEvidence, writeReleaseReport, clearReleaseReport, releaseChecks, reportName, reportSumsName } from './data-api-release-report.mjs'
const hash = data => createHash('sha256').update(data).digest('hex')

async function fixture(t) {
  const directory=await mkdtemp(join(tmpdir(),'gregale-release-report-'))
  t.after(()=>rm(directory,{recursive:true,force:true}))
  const bytes=Buffer.from('fixture artifact'),sha256=hash(bytes)
  const arch={x64:'amd64',arm64:'arm64'}[process.arch]
  const bundle={format:1,contract:1,source:{commit:'a'.repeat(40),epoch:1757000000},tools:{go:'go1.26.9',node:'22.19.0',npm:'10.9.3'},cli:{version:'v0.1.0-test.1',action_sha:'b'.repeat(40),archives:[{file:`gregale_0.1.0-test.1_${process.platform}_${arch}.tar.gz`,bytes:bytes.length,sha256,os:process.platform,arch}]},sdk:{file:'gregale-data-0.1.0.tgz',bytes:bytes.length,sha256,name:'@gregale/data',version:'0.1.0',integrity:`sha512-${createHash('sha512').update(bytes).digest('base64')}`}}
  for(const entry of [...bundle.cli.archives,bundle.sdk])await writeFile(join(directory,entry.file),bytes)
  const manifest=JSON.stringify(bundle)
  await writeFile(join(directory,'data-api-bundle.json'),manifest)
  const sums=[...bundle.cli.archives,bundle.sdk,{file:'data-api-bundle.json',sha256:hash(manifest)}].sort((a,b)=>a.file<b.file?-1:1).map(entry=>`${entry.sha256}  ${entry.file}\n`).join('')
  await writeFile(join(directory,'DATA-API-SHA256SUMS'),sums)
  return {directory,bundle}
}

test('reports bind complete passing checks to actual artifact bytes and identify the executed host target',async t=>{
  const {directory,bundle}=await fixture(t)
  const report=await writeReleaseReport(directory,bundle,releaseChecks,{older:'c'.repeat(64),current:'d'.repeat(64),token:'must-not-serialize'},{older:'e'.repeat(64),current:'f'.repeat(64)},examples)
  assert.equal(report.status,'passed')
  assert.equal(report.source_commit,bundle.source.commit)
  assert.equal(report.artifacts.length,3)
  assert.equal(report.executed_target,`${bundle.cli.archives[0].os}/${bundle.cli.archives[0].arch}`)
  const bytes=await readFile(join(directory,reportName))
  assert.doesNotMatch(bytes.toString(),/must-not-serialize|token/)
  assert.equal(await readFile(join(directory,reportSumsName),'utf8'),`${hash(bytes)}  ${reportName}\n`)
  await clearReleaseReport(directory)
  await assert.rejects(access(join(directory,reportName)))
  await assert.rejects(access(join(directory,reportSumsName)))
})

test('missing checks, changed archives, changed metadata and incomplete checksum lists cannot produce a passed report',async t=>{
  const {directory,bundle}=await fixture(t)
  const contracts={older:'c'.repeat(64),current:'d'.repeat(64)}
  await assert.rejects(writeReleaseReport(directory,bundle,releaseChecks.slice(1),contracts,contracts,examples))
  await assert.rejects(writeReleaseReport(directory,bundle,releaseChecks,contracts),/Custom-project/ )
  await assert.rejects(writeReleaseReport(directory,bundle,releaseChecks,contracts,contracts),/packaged example/)
  await assert.rejects(access(join(directory,reportName)))
  const file=join(directory,bundle.sdk.file),original=await readFile(file)
  await writeFile(file,Buffer.alloc(original.length))
  await assert.rejects(writeReleaseReport(directory,bundle,releaseChecks,contracts,contracts,examples),/checksum/)
  await writeFile(file,original)
  const manifest=join(directory,'data-api-bundle.json'),originalManifest=await readFile(manifest)
  await writeFile(manifest,JSON.stringify({...bundle,source:{...bundle.source,commit:'e'.repeat(40)}}))
  await assert.rejects(bundleEvidence(directory,bundle),/changed/)
  await writeFile(manifest,originalManifest)
  await writeFile(join(directory,'DATA-API-SHA256SUMS'),'')
  await assert.rejects(writeReleaseReport(directory,bundle,releaseChecks,contracts,contracts,examples),/checksum list/)
  await assert.rejects(access(join(directory,reportName)))
})
