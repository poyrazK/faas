import assert from 'node:assert/strict'
import { spawn, spawnSync } from 'node:child_process'
import { readdir, readFile, writeFile, copyFile } from 'node:fs/promises'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { setTimeout as delay } from 'node:timers/promises'
import { run } from '../cmd/gregale/templates/data-api-starter/tools/artifact-lib.mjs'

export async function rolloutMigrationVersions(directory) {
  const names = await readdir(directory)
  const latest = Math.max(0, ...names.flatMap(name => {
    const match = /^(\d{4})_[a-z0-9_]+\.sql$/.exec(name)
    return match ? [Number(match[1])] : []
  }))
  const versions = [latest + 1, latest + 2].map(version => String(version).padStart(4, '0'))
  assert.ok(versions.every(version => /^\d{4}$/.test(version)), 'Rollout fixture needs two unused four-digit migration versions')
  return versions
}

async function localSession(cli, app, env) {
  const child = spawn(cli,['--json','data-api','dev','--port','0','--watch=false','--accepted-contracts','accepted-contracts.json'],{cwd:app,env,stdio:['ignore','pipe','pipe']})
  let pending='', bytes=0, ready, failure=false
  const completion = new Promise(resolve=>{child.once('error',()=>{failure=true;resolve({code:null})});child.once('exit',code=>resolve({code}))})
  child.stdout.on('data',data=>{
    bytes+=data.length
    if(bytes>1048576){failure=true;child.kill('SIGINT');return}
    pending+=data
    for(;;){const end=pending.indexOf('\n');if(end<0)break;const line=pending.slice(0,end);pending=pending.slice(end+1);try{const event=JSON.parse(line);if(event.status==='ready')ready=event}catch{failure=true}}
  })
  child.stderr.resume() // Never copy child diagnostics, tokens or responses into reports.
  const close=async()=>{
    if(child.exitCode===null&&child.pid)child.kill('SIGINT')
    let timer
    const result=await Promise.race([completion,new Promise(resolve=>{timer=setTimeout(()=>resolve({code:null}),210000)})]).finally(()=>clearTimeout(timer))
    if(result.code===null&&child.exitCode===null)child.kill('SIGKILL')
    assert.equal(result.code,0,'Packaged local session must exit cleanly')
  }
  try {
    const deadline=Date.now()+180000
    while(!ready){assert.ok(!failure&&child.exitCode===null,'Packaged local session failed before readiness');assert.ok(Date.now()<deadline,'Packaged local session readiness timed out');await delay(100)}
    return {ready,close}
  } catch(error){try{await close()}catch{};throw error}
}

export async function verifyBundleRollout(app,cli,env,profile = {}) {
  const p = { table:'notes', valueColumn:'body', identities:['alice','bob'],
    body:(identity,value)=>({subject:identity,body:value}),
    values:['release-rollout-replay','release-sdk-old','release-sdk-current','release-must-not-write','release-must-not-rpc','release-sdk-rpc'],
    rpc:'create_note_with_tags', rpcArgs:value=>({note_body:value,tag_names:[]}),
    migrations:'migrations/sql', client:'client', types:'client/src/database.types.ts', ...profile }
  const versions = await rolloutMigrationVersions(join(app,p.migrations))
  const [identityA,identityB] = p.identities
  const path = `/rest/v1/${p.table}`
  let olderTypesReady = !p.keepOlderTypes
  const once=extra=>run(cli,['--json','data-api','dev','--port','0','--once',...(p.checkTypes && olderTypesReady ? ['--check'] : []),...extra],{cwd:app,env,timeout:180000})
  const contractPath=join(app,'.gregale/data-api-contract.json')
  once([])
  await copyFile(contractPath,join(app,'older-schema.json'))
  const older=JSON.parse(await readFile(contractPath,'utf8')).fingerprint
  if(p.keepOlderTypes) { await copyFile(join(app,p.types),join(app,p.client,'older.database.types.ts')); olderTypesReady=true }
  await writeFile(join(app,p.migrations,`${versions[0]}_release_rollout.sql`),`ALTER TABLE api.${p.table} ADD COLUMN release_label text;\n`)
  once([])
  const current=JSON.parse(await readFile(contractPath,'utf8')).fingerprint
  assert.notEqual(older,current)
  run(cli,['data-api','diff','--baseline','older-schema.json','--current',contractPath,'--accept-contract','accepted-contracts.json'],{cwd:app,env})
  const policy=JSON.parse(await readFile(join(app,'accepted-contracts.json'),'utf8'))
  assert.deepEqual(policy,{version:1,current_fingerprint:current,accepted_fingerprints:[older]})
  const rejected=(current[0]==='a'?'b':'a')+current.slice(1)
  const request=(name,hash,identity,method,path,expect,extra={})=>({name,expectedSchemaFingerprint:hash,identity,method,path,expect,...extra})
  const collection={version:2,scenarios:[{name:'mixed-clients',requests:[
    request('old client inserts',older,identityA,'POST',path,{status:201},{body:p.body(identityA,p.values[0]),capture:{id:'/0/id'}}),
    request('current client respects RLS',current,identityB,'GET',`${path}?id=eq.{{id}}`,{status:200,json:[]}),
    request('unapproved client rejected',rejected,identityA,'POST',path,{status:409},{body:p.body(identityA,p.values[3])}),
    request('old client deletes',older,identityA,'DELETE',`${path}?id=eq.{{id}}`,{status:200}),
  ]}]}
  await writeFile(join(app,'rollout.requests.json'),JSON.stringify(collection))
  const replay=once(['--accepted-contracts','accepted-contracts.json','--replay','rollout.requests.json']).trim().split('\n').map(line=>JSON.parse(line))
  assert.deepEqual(replay.find(event=>event.event==='replay_summary'),{event:'replay_summary',total:4,passed:4,failed:0})
  // Import the installed tarball, not the repository SDK or test fixtures.
  const {createDataClient}=await import(pathToFileURL(join(app,p.client,'node_modules/@gregale/data/dist/index.js')))
  const session=await localSession(cli,app,env)
  try {
    const identities=JSON.parse(await readFile(session.ready.identities_file,'utf8')).identities
    const client=(hash,identity)=>createDataClient({url:session.ready.url,expectedSchemaFingerprint:hash,accessToken:identities[identity].token}).schema('api')
    const old=client(older,identityA), fresh=client(current,identityB), bad=client(rejected,identityA)
    const inserted=await old.from(p.table).insert(p.body(identityA,p.values[1])).select('id').single()
    assert.equal(inserted.error,null)
    const visible=await fresh.from(p.table).select('id').eq('id',inserted.data.id)
    assert.equal(visible.error,null);assert.deepEqual(visible.data,[])
    if(p.wrongTenantIdentity) {
      const sameSubjectOtherTenant=await client(older,p.wrongTenantIdentity).from(p.table).select('id').eq('id',inserted.data.id)
      assert.equal(sameSubjectOtherTenant.error,null);assert.deepEqual(sameSubjectOtherTenant.data,[])
    }
    const currentWrite=await fresh.from(p.table).insert(p.body(identityB,p.values[2])).select('id').single()
    assert.equal(currentWrite.error,null)
    const olderIsolation=await old.from(p.table).select('id').eq('id',currentWrite.data.id)
    assert.equal(olderIsolation.error,null);assert.deepEqual(olderIsolation.data,[])
    assert.equal((await fresh.from(p.table).delete().eq('id',currentWrite.data.id)).error,null)
    for(const result of [await bad.from(p.table).insert(p.body(identityA,p.values[3])),await bad.rpc(p.rpc,p.rpcArgs(p.values[4]))]) {
      assert.equal(result.status,409);assert.equal(result.error?.code,'schema_contract_mismatch');assert.equal(result.data,null)
    }
    const unchanged=await old.from(p.table).select('id').in(p.valueColumn,[p.values[3],p.values[4]])
    assert.equal(unchanged.error,null);assert.deepEqual(unchanged.data,[])
    const rpc=await old.rpc(p.rpc,p.rpcArgs(p.values[5]))
    assert.equal(rpc.error,null)
    assert.ok(Array.isArray(rpc.data) && rpc.data.some(row=>row[p.valueColumn]===p.values[5] && Object.entries(p.rpcExpected ?? {}).every(([key,value])=>row[key]===value)),'Project-owned RPC must return its committed row under the caller identity')
    const updated=await old.from(p.table).update({[p.valueColumn]:p.values[0]}).eq('id',inserted.data.id).select(p.valueColumn).single()
    assert.equal(updated.error,null);assert.equal(updated.data[p.valueColumn],p.values[0])
    assert.equal((await old.from(p.table).delete().eq('id',inserted.data.id)).error,null)
  } finally {await session.close()}
  const revoked={...policy,accepted_fingerprints:[rejected]}
  await writeFile(join(app,'accepted-contracts.json'),JSON.stringify(revoked))
  const revokedCollection={version:1,requests:[request('revoked client rejected',older,identityA,'POST',path,{status:409},{body:p.body(identityA,p.values[3])}),request('current client accepted',current,identityA,'GET',path,{status:200})]}
  await writeFile(join(app,'revoked.requests.json'),JSON.stringify(revokedCollection))
  const revokedReplay=once(['--accepted-contracts','accepted-contracts.json','--replay','revoked.requests.json']).trim().split('\n').map(line=>JSON.parse(line))
  assert.equal(revokedReplay.find(event=>event.event==='replay_summary')?.failed,0)
  const typesPath=join(app,p.types)
  const types=await readFile(typesPath),contract=await readFile(contractPath)
  await writeFile(join(app,p.migrations,`${versions[1]}_release_stale.sql`),`ALTER TABLE api.${p.table} ADD COLUMN later_label text;\n`)
  const stale=spawnSync(cli,['--json','data-api','dev','--port','0','--once','--accepted-contracts','accepted-contracts.json'],{cwd:app,env,encoding:'utf8',timeout:180000,maxBuffer:1048576})
  assert.equal(stale.status,1,'Stale policies must reject the next schema')
  assert.ok(/Accepted contracts are stale/.test(stale.stderr ?? ''),'Stale-policy failure must be identified explicitly, rather than accepting an unrelated command failure')
  assert.deepEqual(await readFile(typesPath),types);assert.deepEqual(await readFile(contractPath),contract)
  return {older,current}
}
