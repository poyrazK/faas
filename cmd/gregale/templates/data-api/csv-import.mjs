import {decodeJsonCell} from './csv-export.mjs'
import {validateRecord} from './inspector-data.mjs'
export const csvLimits=Object.freeze({bytes:1048576,records:1000,columns:256,batchRows:25,batchBytes:65536,errors:100})
export function parseImportCsv(source){
 if(typeof source!=='string'||new TextEncoder().encode(source).length>csvLimits.bytes)throw new Error('Use a CSV file of at most 1 MiB.')
 source=source.replace(/^\uFEFF/,'')
 const rows=[];let row=[],field='',quoted=false,closed=false
 const cell=()=>{row.push(field);field='';closed=false;if(row.length>csvLimits.columns)throw new Error('Use at most 256 CSV columns.')}
 const record=()=>{cell();rows.push(row);row=[];if(rows.length>csvLimits.records+1)throw new Error('Use at most 1,000 data records.')}
 for(let index=0;index<source.length;index++){
  const character=source[index]
  if(quoted){if(character==='"'){if(source[index+1]==='"'){field+='"';index++}else{quoted=false;closed=true}}else field+=character;continue}
  if(character===','){cell();continue}
  if(character==='\r'||character==='\n'){if(character==='\r'&&source[index+1]==='\n')index++;record();continue}
  if(closed)throw new Error('Unexpected text after a quoted CSV field.')
  if(character==='"'){if(field)throw new Error('Quote inside an unquoted CSV field.');quoted=true}else field+=character
 }
 if(quoted)throw new Error('Unclosed quoted CSV field.')
 if(field||closed||row.length)record()
 if(rows.length<2)throw new Error('Include distinct headers and at least one data record.')
 rows[0]=rows[0].map(value=>value.trim())
 if(rows[0].some(value=>!value)||new Set(rows[0]).size!==rows[0].length)throw new Error('Use distinct, nonempty CSV headers.')
 return {headers:rows[0],records:rows.slice(1)}
}
function convert(value,rule){
 while(rule.kind==='nullable')rule=rule.value
 if(['string','enum'].includes(rule.kind))return value
 if(rule.kind==='number'){
  const text=value.trim();if(!/^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$/.test(text))throw new Error('invalid_number')
  return Number(text)
 }
 if(rule.kind==='boolean'){
  const text=value.trim().toLowerCase();if(!['true','false'].includes(text))throw new Error('invalid_boolean');return text==='true'
 }
 try{return JSON.parse(value)}catch{throw new Error('invalid_json')}
}
export function previewCsvImport(dataset,mapping,model,encoding='plain'){
 if(!['plain','json'].includes(encoding))throw new Error('Choose plain CSV values or JSON cells.')
 if(!model?.fields||!model.spec||!Array.isArray(dataset?.headers)||!Array.isArray(dataset.records)||!dataset.records.length||dataset.records.length>csvLimits.records||dataset.headers.length>csvLimits.columns||!Array.isArray(mapping)||mapping.length!==dataset.headers.length)throw new Error('Load a bounded CSV and select a writable table.')
 const fields=model.spec.fields,targets=new Set()
 for(const choice of mapping){
  if(!choice||!['value','omit','null'].includes(choice.empty)||typeof choice.field!=='string')throw new Error('Choose column mappings and blank-cell handling.')
  if(!choice.field)continue
  if(!model.fields.some(field=>field.name===choice.field)||!fields.some(field=>field.name===choice.field)||targets.has(choice.field))throw new Error('Map each writable database column at most once.')
  targets.add(choice.field)
 }
 const errors=[],entries=[];let valid=0,invalid=0
 for(const [index,record] of dataset.records.entries()){
  const number=index+2,payload={};let issue
  if(!Array.isArray(record)||record.length!==dataset.headers.length||record.some(value=>typeof value!=='string'))issue={code:'column_count',path:[]}
  else for(const [position,choice] of mapping.entries()){
   if(!choice.field)continue
   const field=fields.find(field=>field.name===choice.field),cell=record[position]
   if(cell===''&&choice.empty==='omit')continue
   let value
   try{value=cell===''&&choice.empty==='null'?null:encoding==='json'?decodeJsonCell(cell):convert(cell,field.value)}catch(error){issue={code:error.message==='precision_loss'?'precision_loss':encoding==='json'?'invalid_json':error.message,path:[field.name]};break}
   Object.defineProperty(payload,field.name,{value,enumerable:true})
  }
  let json
  if(!issue){const checked=validateRecord(payload,model.spec);if(!checked.ok)issue=checked.issue;else{json=JSON.stringify(checked.value);if(new TextEncoder().encode(json).length+2>csvLimits.batchBytes)issue={code:'row_too_large',path:[]}}}
  if(issue){invalid++;if(errors.length<csvLimits.errors)errors.push({record:number,code:issue.code,path:issue.path})}
  else{valid++;entries.push(Object.freeze({record:number,json}))}
 }
 if(invalid)return {ok:false,total:dataset.records.length,valid,invalid,errors,truncated:invalid>errors.length}
 return {ok:true,total:valid,valid,invalid:0,errors:[],entries:Object.freeze(entries)}
}
export function csvBatches(entries){
 const batches=[];let current=[],bytes=2
 for(const entry of entries){
  const size=new TextEncoder().encode(entry.json).length
  if(size+2>csvLimits.batchBytes)throw new Error('An import row exceeds the batch byte limit.')
  if(current.length&&(current.length>=csvLimits.batchRows||bytes+size+1>csvLimits.batchBytes)){batches.push(current);current=[];bytes=2}
  bytes+=size+(current.length?1:0);current.push(entry)
 }
 if(current.length)batches.push(current)
 return batches
}
const transactionRefusal=code=>typeof code==='string'&&(/^[0-9A-Z]{5}$/.test(code)&&!['00','01','02'].includes(code.slice(0,2))||/^PGRST[0-9]{3}$/.test(code)||['schema_contract_mismatch','invalid_token','authentication_required'].includes(code))
export function importOutcome(response,expectedRows){
 if(response?.status===201&&!response.rollback&&Number.isInteger(expectedRows)&&response.count===expectedRows)return 'confirmed'
 if([400,401,403,404,405,406,409,413,415,416,422].includes(response?.status)&&transactionRefusal(response.code))return 'refused'
 return 'uncertain'
}
export function importSummary(rows){
 return Object.fromEntries(['confirmed','refused','uncertain','not_sent','pending'].map(outcome=>[outcome,rows.filter(row=>row.outcome===outcome).length]))
}
export function retryableImportRows(entries,report){
 const eligible=new Set(report.filter(row=>['refused','not_sent'].includes(row.outcome)).map(row=>row.record))
 return entries.filter(entry=>eligible.has(entry.record))
}
export async function runCsvImport(entries,{sendBatch,current=()=>true,signal,onProgress=()=>{}}){
 const rows=entries.map(entry=>({record:entry.record,outcome:'not_sent'}))
 const publish=()=>onProgress(rows.map(row=>({...row})))
 publish()
 for(const batch of csvBatches(entries)){
  if(signal?.aborted||!current())break
  const records=new Set(batch.map(entry=>entry.record));for(const row of rows)if(records.has(row.record))row.outcome='pending';publish()
  let result
  try{result=await sendBatch(`[${batch.map(entry=>entry.json).join(',')}]`,signal)}catch{result=null}
  const outcome=importOutcome(result,batch.length)
  for(const row of rows)if(records.has(row.record)){row.outcome=outcome;if(Number.isInteger(result?.status))row.status=result.status;if(outcome==='uncertain'&&Number.isInteger(result?.count))row.acknowledged_batch_rows=result.count}
  publish()
  if(outcome==='uncertain'||result?.code==='schema_contract_mismatch')break
 }
 return {rows,summary:importSummary(rows)}
}
