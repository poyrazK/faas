import {validateViews,viewProblem} from './inspector-data.mjs'
const fields=(value,keys)=>value&&typeof value==='object'&&!Array.isArray(value)&&Object.keys(value).every(key=>keys.includes(key))
const identifier=value=>typeof value==='string'&&/^[A-Za-z][A-Za-z0-9_-]{0,63}$/.test(value)
const column=value=>typeof value==='string'&&/^[A-Za-z_][A-Za-z0-9_]*$/.test(value)&&!['select','limit','offset','order','and','or','not'].includes(value)
const scalar=value=>['string','boolean','number'].includes(typeof value)&&(typeof value!=='string'||value.length<=1024)&&(typeof value!=='number'||Number.isSafeInteger(value))
export function accessScaffold(view,identities){
 validateViews({version:1,views:[view]})
 const manifest={version:3,status:'needs_review',view:JSON.parse(JSON.stringify(view)),fixtures:view.steps.map((step,index)=>({name:`step_${index+1}`,schema:step.schema,table:step.table,keys:null})),checks:identities.map(identity=>({identity,steps:view.steps.map((_,index)=>({fixture:`step_${index+1}`,rows:null}))}))}
 validateGuidedAccess(manifest,identities);return manifest
}
export function validateGuidedAccess(value,identities){
 const fail=()=>{throw new Error('Invalid guided access checks; review fixture keys and per-identity row counts.')}
 if(!fields(value,['version','status','view','fixtures','checks'])||value.version!==3||!['needs_review','reviewed'].includes(value.status))fail()
 validateViews({version:1,views:[value.view]})
 const length=value.view.steps.length
 if(!Array.isArray(value.fixtures)||value.fixtures.length!==length||!Array.isArray(value.checks)||!value.checks.length||value.checks.length*length>100)fail()
 const fixtures=new Set(),names=new Set()
 for(const [index,fixture] of value.fixtures.entries()){
  const step=value.view.steps[index]
  if(!fields(fixture,['name','schema','table','keys'])||!identifier(fixture.name)||fixtures.has(fixture.name)||fixture.schema!==step.schema||fixture.table!==step.table)fail()
  fixtures.add(fixture.name)
  if(fixture.keys===null){if(value.status==='reviewed')fail()}
  else if(!fields(fixture.keys,Object.keys(fixture.keys??{}))||!Object.keys(fixture.keys).length||Object.keys(fixture.keys).length>16||Object.entries(fixture.keys).some(([name,value])=>!column(name)||!scalar(value)))fail()
 }
 for(const check of value.checks){
  if(!fields(check,['identity','steps'])||!identifier(check.identity)||names.has(check.identity)||identities&&!identities.includes(check.identity)||!Array.isArray(check.steps)||check.steps.length!==length)fail()
  names.add(check.identity)
  for(const [index,step] of check.steps.entries())if(!fields(step,['fixture','rows'])||step.fixture!==value.fixtures[index].name||!(step.rows===null&&value.status==='needs_review')&&(!Number.isInteger(step.rows)||step.rows<0||step.rows>50))fail()
 }
 if(value.status==='reviewed'&&value.view.steps.some((_,index)=>!value.checks.some(check=>check.steps[index].rows>0)))throw new Error('Each relationship step needs a positive visibility expectation to detect missing fixtures.')
 if(new TextEncoder().encode(JSON.stringify(value)).length>65536)fail()
 return value
}
export function guidedPlan(manifest,snapshot,fingerprint,identities){
 validateGuidedAccess(manifest,identities)
 if(manifest.status!=='reviewed')throw new Error('Review fixture keys and expectations and set status to reviewed before running.')
 const problem=viewProblem(manifest.view,snapshot,fingerprint);if(problem)throw new Error(problem)
 const plans=manifest.view.steps.map((step,index)=>{
  const table=snapshot.tables.find(table=>table.schema===step.schema&&table.name===step.table),keys=manifest.fixtures[index].keys
  if(Object.keys(keys).some(name=>!table.columns.some(column=>column.name===name)))throw new Error('Fixture references an unavailable column.')
  if(table.primary_key?.filterable&&table.primary_key.columns.some(name=>!Object.hasOwn(keys,name)))throw new Error('Fixture keys must include the full declared primary key.')
  return {step,keys:{...keys}}
 })
 for(let index=1;index<plans.length;index++){
  const previous=plans[index-1],current=plans[index],via=current.step.via
  const child=via.direction==='parent'?snapshot.tables.find(table=>table.schema===previous.step.schema&&table.name===previous.step.table):snapshot.tables.find(table=>table.schema===current.step.schema&&table.name===current.step.table)
  const relation=child.relationships.find(relation=>relation.foreignKeyName===via.foreignKey&&relation.referencedRelation===(via.direction==='parent'?current.step.table:previous.step.table))
  const from=via.direction==='parent'?relation.columns:relation.referencedColumns,to=via.direction==='parent'?relation.referencedColumns:relation.columns
  for(let key=0;key<from.length;key++)if(!Object.hasOwn(previous.keys,from[key])||!Object.hasOwn(current.keys,to[key])||previous.keys[from[key]]!==current.keys[to[key]])throw new Error('Named fixtures must include matching values for every composite relationship key.')
 }
 return plans
}
export async function runGuidedAccess(manifest,{url,identities,snapshot,fingerprint,signal,fetcher=fetch}){
 const bases=manifest.checks.flatMap(check=>check.steps.map((expected,index)=>({scenario:manifest.view.name,name:`Step ${index+1}: ${manifest.view.steps[index].schema}.${manifest.view.steps[index].table}`,step:index+1,identity:check.identity,fixture:expected.fixture,expected_rows:expected.rows})))
 let plans
 try{plans=guidedPlan(manifest,snapshot,fingerprint,Object.keys(identities))}catch(error){return bases.map(base=>({...base,passed:false,skipped:true,error:error.message}))}
 const results=[]
 for(const base of bases){
  signal?.throwIfAborted()
  const {step,keys}=plans[base.step-1],target=new URL(`/__gregale/contract/${manifest.view.fingerprint}/rest/v1/${encodeURIComponent(step.table)}`,url)
  // Fetch only one known field; reports contain counts, never row contents.
  target.searchParams.set('select',JSON.stringify(step.columns[0]));target.searchParams.set('limit','51')
  for(const [name,value] of Object.entries(keys))target.searchParams.append(name,`eq.${String(value)}`)
  if(step.filter)target.searchParams.append(step.filter.column,`${step.filter.operator}.${step.filter.value}`)
  try{
   const response=await fetcher(target,{method:'GET',redirect:'error',headers:{Authorization:`Bearer ${identities[base.identity].token}`,'Accept-Profile':step.schema},signal:signal?AbortSignal.any([signal,AbortSignal.timeout(30000)]):AbortSignal.timeout(30000)})
   const reader=response.body?.getReader();let size=0;const chunks=[]
   try{if(reader)for(;;){const {done,value}=await reader.read();if(done)break;size+=value.length;if(size>1048576)throw new Error('Response too large');chunks.push(value)}}finally{if(reader){await reader.cancel().catch(()=>{});reader.releaseLock()}}
   const bytes=new Uint8Array(size);let offset=0;for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.length}
   const rows=JSON.parse(new TextDecoder().decode(bytes))
   const valid=response.status===200&&Array.isArray(rows)&&rows.every(row=>row&&typeof row==='object'&&!Array.isArray(row))
   const passed=valid&&rows.length===base.expected_rows
   results.push({...base,passed,status:response.status,...(valid?{visible_rows:rows.length}:{}),...(!passed?{error:'Visibility expectation failed; inspect this identity and relationship step.'}:{})})
  }catch{signal?.throwIfAborted();results.push({...base,passed:false,error:'Read failed, timed out or exceeded response limits.'})}
 }
 return results
}
