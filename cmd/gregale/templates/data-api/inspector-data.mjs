// Pure validation for the inspector's server-generated descriptor model. No SQL
// or input values enter errors. Keep parity with generated TypeScript parsers.
export function validateRecord(input,spec){
 let nodes=0;const active=new Set()
 const fail=(code,path)=>{throw Object.freeze({code,path:Object.freeze([...path])})}
 function visit(value,rule,depth,path){
  if(++nodes>100000||depth>64)fail('input_too_complex',path)
  if(rule.kind==='nullable')return value===null?null:visit(value,rule.value,depth+1,path)
  if(['string','number','boolean','enum'].includes(rule.kind)){
   if(typeof value!==(rule.kind==='enum'?'string':rule.kind))fail('invalid_type',path)
   if(rule.kind==='enum'&&!rule.labels.includes(value))fail('invalid_enum',path)
   if(typeof value==='number'){
    if(!Number.isFinite(value))fail('not_finite',path)
    if(rule.integer&&!Number.isSafeInteger(value))fail('invalid_integer',path)
    if(rule.min!==undefined&&value<rule.min||rule.max!==undefined&&value>rule.max)fail('out_of_range',path)
   }
   if(typeof value==='string'&&rule.maxLength!==undefined&&[...value].length>rule.maxLength)fail('too_long',path)
   return value
  }
  const json=['Json','unsupported'].includes(rule.kind)
  if(json&&(value===null||typeof value==='string'||typeof value==='boolean'||typeof value==='number'&&Number.isFinite(value)))return value
  if(!value||typeof value!=='object')fail(json?'invalid_json':'invalid_type',path)
  if(active.has(value))fail('cyclic_input',path)
  active.add(value)
  try{
   if(Array.isArray(value)){
    if(!json&&rule.kind!=='array')fail('invalid_type',path)
    const descriptors=Object.getOwnPropertyDescriptors(value)
    if(Object.getOwnPropertySymbols(value).length||Object.keys(descriptors).some(key=>key!=='length'&&(!/^(0|[1-9][0-9]*)$/.test(key)||!('value' in descriptors[key])||!descriptors[key].enumerable)))fail('unsupported_object',path)
    return Array.from({length:value.length},(_,index)=>visit(descriptors[String(index)]?.value,json?{kind:'Json'}:rule.value,depth+1,json?path:[...path,index]))
   }
   if(!json&&rule.kind!=='object')fail('invalid_type',path)
   if(![null,Object.prototype].includes(Object.getPrototypeOf(value))||Object.getOwnPropertySymbols(value).length)fail('unsupported_object',path)
   const descriptors=Object.getOwnPropertyDescriptors(value),output={},fields=rule.fields??[]
   for(const key of Object.keys(descriptors)){
    const field=fields.find(field=>field.name===key)
    if(!json&&!field)fail('unknown_field',path)
    const fieldPath=json?path:[...path,field.name],descriptor=descriptors[key]
    if(!('value' in descriptor)||!descriptor.enumerable)fail('unsupported_object',fieldPath)
    if(!json&&descriptor.value===null&&!field.nullable)fail('null_not_allowed',fieldPath)
    const parsed=field?.nullable&&descriptor.value===null?null:visit(descriptor.value,json?{kind:'Json'}:field.value,depth+1,fieldPath)
    Object.defineProperty(output,key,{value:parsed,enumerable:true,writable:true,configurable:true})
   }
   for(const field of fields)if(!field.optional&&!Object.hasOwn(descriptors,field.name))fail('required',[...path,field.name])
   return output
  }finally{active.delete(value)}
 }
 try{return {ok:true,value:visit(input,spec,0,[])}}catch(issue){return {ok:false,issue:{code:issue?.code??'invalid_input',path:issue?.path??[]}}}
}

const reserved=new Set(['select','limit','offset','order','and','or','not'])
export function keyedEdit(model,row){
 if(!model?.primaryKey?.filterable||model.concurrency?.kind!=='integer_version')return null
 const columns=[...model.primaryKey.columns,model.concurrency.column]
 if(columns.some(name=>!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)||reserved.has(name)))return null
 const version=row[model.concurrency.column]
 if(!Number.isSafeInteger(version)||version>=Number.MAX_SAFE_INTEGER)return null
 const keys={}
 for(const name of model.primaryKey.columns){
  if(!Object.hasOwn(row,name))return null
  const value=row[name]
  if(!['string','number','boolean'].includes(typeof value)||typeof value==='number'&&!Number.isSafeInteger(value))return null
  Object.defineProperty(keys,name,{value,enumerable:true})
 }
 return {keys,version,column:model.concurrency.column}
}
export function editTarget(plan,origin){
 const target=new URL(`/__gregale/contract/${plan.fingerprint}/rest/v1/${encodeURIComponent(plan.table)}`,origin)
 for(const [name,value] of Object.entries(plan.edit.keys))target.searchParams.set(name,`eq.${typeof value==='string'?JSON.stringify(value):String(value)}`)
 target.searchParams.set(plan.edit.column,`eq.${plan.edit.version}`)
 return target
}

// Only exposed, same-schema foreign keys with complete scalar values can navigate.
export function relationshipLinks(snapshot,table,row){
 const links=[]
 const add=(source,target,relation,reverse)=>{
  const from=reverse?relation.referencedColumns:relation.columns,to=reverse?relation.columns:relation.referencedColumns
  if(!Array.isArray(from)||!Array.isArray(to)||!from.length||from.length>16||from.length!==to.length||new Set(to).size!==to.length)return
  const predicates=[]
  for(let index=0;index<from.length;index++){
   const name=to[index],value=row[from[index]]
   if(!source.columns.some(column=>column.name===from[index])||!target.columns.some(column=>column.name===name)||!Object.hasOwn(row,from[index])||!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)||reserved.has(name))return
   if(!['string','boolean','number'].includes(typeof value)||typeof value==='number'&&!Number.isSafeInteger(value))return
   predicates.push([name,`eq.${String(value)}`])
  }
  links.push({schema:target.schema,table:target.name,predicates,via:{foreignKey:relation.foreignKeyName,direction:reverse?'children':'parent'},label:`${reverse?'Children':'Parent'}: ${target.schema}.${target.name} (${relation.foreignKeyName})`})
 }
 for(const relation of table.relationships??[]){const target=snapshot.tables.find(candidate=>candidate.schema===table.schema&&candidate.name===relation.referencedRelation);if(target)add(table,target,relation,false)}
 for(const child of snapshot.tables.filter(candidate=>candidate.schema===table.schema))for(const relation of child.relationships??[])if(relation.referencedRelation===table.name)add(table,child,relation,true)
 return links
}

// Portable view definitions contain query choices, never identities or row keys.
const viewFields=(value,keys)=>value&&typeof value==='object'&&!Array.isArray(value)&&Object.keys(value).every(key=>keys.includes(key))
const viewName=value=>typeof value==='string'&&value.length>0&&value.length<=100
export function validateViews(value){
 const fail=()=>{throw new Error('Invalid saved view definition.')}
 if(!viewFields(value,['version','views'])||value.version!==1||!Array.isArray(value.views)||value.views.length>100)fail()
 const names=new Set()
 for(const view of value.views){
  if(!viewFields(view,['name','fingerprint','steps','mode','identity'])||!viewName(view.name)||!view.name.trim()||names.has(view.name)||typeof view.fingerprint!=='string'||!/^[a-f0-9]{64}$/.test(view.fingerprint)||!Array.isArray(view.steps)||!view.steps.length||view.steps.length>33)fail()
  if('mode'in view&&(view.mode!=='read_only'||typeof view.identity!=='string'||! /^[A-Za-z][A-Za-z0-9_-]{0,63}$/.test(view.identity))||'identity'in view&&view.mode!=='read_only')fail()
  names.add(view.name)
  for(const [index,step] of view.steps.entries()){
   if(!viewFields(step,['schema','table','columns','filter','sort','via'])||!viewName(step.schema)||!viewName(step.table)||!Array.isArray(step.columns)||!step.columns.length||step.columns.length>1600||step.columns.some(name=>!viewName(name))||new Set(step.columns).size!==step.columns.length)fail()
   if(step.filter!==null&&(!viewFields(step.filter,['column','operator','value'])||!viewName(step.filter.column)||!['eq','neq','gt','gte','lt','lte','is','ilike'].includes(step.filter.operator)||typeof step.filter.value!=='string'||step.filter.value.length>1024))fail()
   if(step.sort!==null&&(!viewFields(step.sort,['column','direction'])||!viewName(step.sort.column)||!['asc','desc'].includes(step.sort.direction)))fail()
   if(index===0?step.via!==null:!viewFields(step.via,['foreignKey','direction'])||!viewName(step.via.foreignKey)||!['parent','children'].includes(step.via.direction))fail()
  }
 }
 if(new TextEncoder().encode(JSON.stringify(value)).length>65536)fail()
 return value
}
export function viewProblem(view,snapshot,fingerprint){
 try{validateViews({version:1,views:[view]})}catch{return 'Invalid saved view definition.'}
 if(view.fingerprint!==fingerprint)return 'Schema changed since this view was saved. Review the path and save an updated view.'
 for(const [index,step] of view.steps.entries()){
  const table=snapshot.tables.find(table=>table.schema===step.schema&&table.name===step.table)
  if(!table||step.columns.some(name=>!table.columns.some(column=>column.name===name)))return 'A saved table or column is unavailable.'
  for(const choice of [step.filter,step.sort])if(choice&&(!table.columns.some(column=>column.name===choice.column)||!/^[A-Za-z_][A-Za-z0-9_]*$/.test(choice.column)||reserved.has(choice.column)))return 'A saved filter or sorting column is unavailable.'
  if(index){
   const previous=view.steps[index-1],child=step.via.direction==='parent'?snapshot.tables.find(table=>table.schema===previous.schema&&table.name===previous.table):table,parent=step.via.direction==='parent'?step:previous
   if(child.schema!==parent.schema||!child.relationships?.some(relation=>relation.foreignKeyName===step.via.foreignKey&&relation.referencedRelation===parent.table&&relation.columns.length>0&&relation.columns.length===relation.referencedColumns.length))return 'A saved relationship is unavailable.'
  }
 }
 return null
}
