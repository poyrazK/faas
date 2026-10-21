import {validateGuidedAccess,guidedPlan,runGuidedAccess} from './guided-access.mjs'
import {matchesJSON} from './request-replay.mjs'
const fields=(value,names)=>value&&typeof value==='object'&&!Array.isArray(value)&&Object.keys(value).every(name=>names.includes(name))
const name=value=>typeof value==='string'&&/^[A-Za-z][A-Za-z0-9_-]{0,63}$/.test(value)
const column=value=>/^[A-Za-z_][A-Za-z0-9_]*$/.test(value)&&!['select','limit','offset','order','and','or','not'].includes(value)
const scalar=value=>['string','number','boolean'].includes(typeof value)&&(typeof value!=='number'||Number.isSafeInteger(value))&&(typeof value!=='string'||value.length<=1024)
export function readManifest(value){const {mutations,mutationFixtures,...read}=value;return {...read,version:3}}
export function mutationScaffold(read){
 validateGuidedAccess(read)
 const first=read.fixtures[0],value={...JSON.parse(JSON.stringify(read)),version:4,status:'needs_review',mutationFixtures:[{name:'created_row',schema:first.schema,table:first.table,keys:null}],mutations:read.checks.flatMap(check=>['POST','PATCH','DELETE','TRANSFER'].map((method,index)=>({name:`${check.identity}_${index+1}`,identity:check.identity,fixture:method==='POST'?'created_row':first.name,method:method==='TRANSFER'?'PATCH':method,body:null,expect:{status:null,rows:null,outcome:null}})))}
 validateMutations(value);return value
}
export function lifecycleScaffold(read){
 validateGuidedAccess(read)
 const first=read.fixtures[0],used=new Set(read.fixtures.map(fixture=>fixture.name))
 const unique=base=>{let value=base,index=1;while(used.has(value))value=`${base}_${index++}`;used.add(value);return value}
 const authorization=unique('authorization_record'),unaffected=unique('unaffected_record')
 const checkpoint=(name,fixture)=>({name,identity:null,fixture,method:'GET',body:null,expect:{status:null,rows:null,outcome:null}})
 const value={...JSON.parse(JSON.stringify(read)),version:5,status:'needs_review',mutationFixtures:[
  {name:authorization,schema:first.schema,table:'REVIEW_MEMBERSHIP_TABLE',keys:null},
  {name:unaffected,schema:first.schema,table:first.table,keys:null}
 ],mutations:[checkpoint('Before_change',first.name),
  {name:'Authorization_change',identity:null,fixture:authorization,method:'PATCH',body:null,expect:{status:null,rows:null,outcome:null}},
  checkpoint('Member_read_after_change',first.name),
  {name:'Member_write_after_change',identity:null,fixture:first.name,method:'PATCH',body:null,expect:{status:null,rows:null,outcome:null}},
  checkpoint('Other_member_retains_access',unaffected)
 ]}
 validateMutations(value);return value
}
export function validateMutations(value,identities){
 const fail=()=>{throw new Error('Invalid mutation checks; review named fixtures, payloads and write expectations.')}
 if(!fields(value,['version','status','view','fixtures','checks','mutationFixtures','mutations'])||![4,5].includes(value.version))fail()
 validateGuidedAccess(readManifest(value),identities)
 if(!Array.isArray(value.mutationFixtures)||value.mutationFixtures.length>100||!Array.isArray(value.mutations)||!value.mutations.length||value.mutations.length+value.checks.length*value.view.steps.length>100)fail()
 const fixtures=new Set(value.fixtures.map(fixture=>fixture.name)),names=new Set()
 for(const fixture of value.mutationFixtures){
  if(!fields(fixture,['name','schema','table','keys'])||!name(fixture.name)||fixtures.has(fixture.name)||typeof fixture.schema!=='string'||!fixture.schema||typeof fixture.table!=='string'||!fixture.table)fail()
  fixtures.add(fixture.name)
  if(fixture.keys===null){if(value.status==='reviewed')fail()}
  else if(!fields(fixture.keys,Object.keys(fixture.keys??{}))||!Object.keys(fixture.keys).length||Object.keys(fixture.keys).length>16||Object.entries(fixture.keys).some(([key,value])=>!column(key)||!scalar(value)))fail()
 }
 for(const check of value.mutations){
  if(!fields(check,['name','identity','fixture','method','body','expect'])||!name(check.name)||names.has(check.name)||!(value.version===5&&value.status==='needs_review'&&check.identity===null)&&(!name(check.identity)||identities&&!identities.includes(check.identity))||!fixtures.has(check.fixture)||!(value.version===5?['GET','POST','PATCH','DELETE']:['POST','PATCH','DELETE']).includes(check.method)||!fields(check.expect,['status','rows','outcome']))fail()
  names.add(check.name)
  if(check.body!==null&&!fields(check.body,Object.keys(check.body??{})))fail()
  if(['GET','DELETE'].includes(check.method)&&check.body!==null)fail()
  if(value.status==='needs_review')continue
  if(check.method==='GET'){
   if(check.expect.status!==200||!Number.isInteger(check.expect.rows)||![0,1].includes(check.expect.rows)||check.expect.outcome!=='visible')fail()
   continue
  }
  if(check.method!=='DELETE'&&(!check.body||!Object.keys(check.body).length))fail()
  const expected=check.expect,allowed={POST:'created',PATCH:'updated',DELETE:'deleted'}
  if(expected.outcome==='unchanged'){
   if(expected.rows!==0||![204,403].includes(expected.status)||check.method==='POST'&&expected.status!==403)fail()
  }else if(expected.outcome!==allowed[check.method]||expected.rows!==1||expected.status!==(check.method==='POST'?201:204))fail()
 }
 if(value.version===5&&value.status==='reviewed'&&(!value.mutations.some(check=>check.method==='GET')||!value.mutations.some(check=>check.method!=='GET')))fail()
 if(new TextEncoder().encode(JSON.stringify(value)).length>65536)fail()
 return value
}
export function mutationPlan(manifest,{snapshot,fingerprint,identities,disposable,observe}){
 validateMutations(manifest,Object.keys(identities))
 if(!disposable||typeof observe!=='function')throw new Error('Mutation checks require the disposable verifier and its read-only state observer.')
 guidedPlan(readManifest(manifest),snapshot,fingerprint,Object.keys(identities))
 const fixtures=new Map([...manifest.fixtures,...manifest.mutationFixtures].map(fixture=>[fixture.name,{...fixture,keys:{...fixture.keys}}]))
 for(const fixture of fixtures.values()){
  const table=snapshot.tables.find(table=>table.schema===fixture.schema&&table.name===fixture.table)
  if(!table||table.view||Object.keys(fixture.keys).some(key=>!table.columns.some(column=>column.name===key)))throw new Error('Mutation fixtures require known physical tables and scalar selector columns.')
 }
 for(const check of manifest.mutations){
  const fixture=fixtures.get(check.fixture),table=snapshot.tables.find(table=>table.schema===fixture.schema&&table.name===fixture.table)
  for(const key of Object.keys(check.body??{})){
   const column=table.columns.find(column=>column.name===key)
   if(!column||(check.method==='POST'?column.insertable:column.updatable)===false)throw new Error('Mutation payload references an unavailable or non-writable column.')
  }
  if(check.method==='POST'&&Object.entries(fixture.keys).some(([key,value])=>!Object.hasOwn(check.body??{},key)||!matchesJSON(check.body[key],value)))throw new Error('Create payload must include every named fixture selector value.')
  if(['PATCH','DELETE'].includes(check.method)&&(!table.primary_key?.filterable||table.primary_key.columns.some(key=>!Object.hasOwn(fixture.keys,key))))throw new Error('Updates and deletes require every declared filterable primary-key column.')
 }
 return fixtures
}
async function responseJSON(response){
 const reader=response.body?.getReader(),chunks=[];let size=0
 try{if(reader)for(;;){const {done,value}=await reader.read();if(done)break;size+=value.length;if(size>1048576)throw new Error('Response exceeds bounds');chunks.push(value)}}finally{if(reader){await reader.cancel().catch(()=>{});reader.releaseLock()}}
 const bytes=new Uint8Array(size);let offset=0;for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.length}
 return size?JSON.parse(new TextDecoder().decode(bytes)):null
}
export async function runGuidedMutations(manifest,context){
 const bases=manifest.mutations.map(check=>({scenario:manifest.view.name,name:check.name,identity:check.identity,fixture:check.fixture,operation:check.method}))
 let fixtures
 try{fixtures=mutationPlan(manifest,context)}catch(error){return bases.map(base=>({...base,passed:false,skipped:true,error:error.message}))}
 const reads=await runGuidedAccess(readManifest(manifest),context)
 if(reads.some(result=>!result.passed))return [...reads,...bases.map(base=>({...base,passed:false,skipped:true,error:'Read baseline failed; mutations were not executed.'}))]
 const results=[...reads];let blocked=false
 for(const [index,check] of manifest.mutations.entries()){
  context.signal?.throwIfAborted();const base=bases[index]
  if(blocked){results.push({...base,passed:false,skipped:true,error:'An earlier operation failed; remaining lifecycle steps were not executed.'});continue}
  try{
   const fixture=fixtures.get(check.fixture),before=await context.observe(fixture)
   const expectedBefore=check.method==='POST'?0:1
   if(before.rows.length!==expectedBefore)throw new Error('Named fixture cardinality is invalid before the write.')
   if(check.method==='PATCH'&&Object.entries(check.body).every(([key,value])=>matchesJSON(before.rows[0][key],value)))throw new Error('Update payload must attempt a change to the synthetic fixture.')
   const target=new URL(`/__gregale/contract/${manifest.view.fingerprint}/rest/v1/${encodeURIComponent(fixture.table)}`,context.url)
   if(check.method!=='POST')for(const [key,value] of Object.entries(fixture.keys))target.searchParams.append(key,`eq.${String(value)}`)
   if(check.method==='GET'){
    target.searchParams.set('select',JSON.stringify(Object.keys(fixture.keys)[0]));target.searchParams.set('limit','2')
    const response=await (context.fetcher??fetch)(target,{method:'GET',redirect:'error',headers:{Authorization:`Bearer ${context.identities[check.identity].token}`,'Accept-Profile':fixture.schema},signal:context.signal?AbortSignal.any([context.signal,AbortSignal.timeout(30000)]):AbortSignal.timeout(30000)})
    const json=await responseJSON(response),after=await context.observe(fixture)
    const valid=response.status===200&&Array.isArray(json)&&json.every(row=>row&&typeof row==='object'&&!Array.isArray(row))
    const unchanged=before.signature===after.signature,passed=valid&&json.length===check.expect.rows&&unchanged&&after.rows.length===1
    results.push({...base,checkpoint:true,passed,status:response.status,expected_rows:check.expect.rows,...(valid?{visible_rows:json.length}:{}),state_matches:unchanged,...(!passed?{error:'Authorization checkpoint failed; inspect the existing-token identity and named fixture.'}:{})})
    blocked=!passed;continue
   }
   const response=await (context.fetcher??fetch)(target,{method:check.method,redirect:'error',...(check.method==='DELETE'?{}:{body:JSON.stringify(check.body)}),headers:{Authorization:`Bearer ${context.identities[check.identity].token}`,'Content-Type':'application/json','Content-Profile':fixture.schema,Prefer:'return=minimal'},signal:context.signal?AbortSignal.any([context.signal,AbortSignal.timeout(30000)]):AbortSignal.timeout(30000)})
   const json=await responseJSON(response)
   const afterKeys=check.method==='PATCH'?Object.fromEntries(Object.entries(fixture.keys).map(([key,value])=>[key,Object.hasOwn(check.body,key)?check.body[key]:value])):fixture.keys
   const after=await context.observe({...fixture,keys:afterKeys})
   const statusMatches=response.status===check.expect.status
   const responseMatches=check.expect.status===403?json?.code==='42501':json===null
   let stateMatches
   if(check.expect.outcome==='unchanged')stateMatches=before.signature===after.signature
   else if(check.method==='DELETE')stateMatches=after.rows.length===0
   else stateMatches=after.rows.length===1&&Object.entries(check.body).every(([key,value])=>matchesJSON(after.rows[0][key],value))&&(check.method==='POST'||!matchesJSON(before.rows[0],after.rows[0]))
   const passed=statusMatches&&responseMatches&&stateMatches
   results.push({...base,passed,status:response.status,state_matches:stateMatches,...(check.expect.outcome==='unchanged'?{unchanged:stateMatches}:{}),...(!passed?{error:'Mutation expectation failed; inspect the identity, operation and fixture state.'}:{})})
   if(passed&&check.method==='PATCH'&&check.expect.outcome==='updated')fixture.keys=afterKeys
   blocked=!passed
  }catch{context.signal?.throwIfAborted();results.push({...base,passed:false,error:'Mutation or state observation failed; remaining writes were stopped.'});blocked=true}
 }
 return results
}
