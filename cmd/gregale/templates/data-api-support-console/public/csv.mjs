export const exportLimits=Object.freeze({rows:10000,pageRows:100,columns:256,bytes:10485760,pageBytes:1048576,nodes:100000,depth:64})
const simple=name=>typeof name==='string'&&/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)&&!['select','limit','offset','order','and','or','not'].includes(name)
const numberPattern=/^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?/
class ExactNumber{constructor(source){this.source=source;Object.freeze(this)}}
// Preserve numeric lexemes, including int8/numeric values beyond JS precision.
export function parseExactJSON(source){
 if(typeof source!=='string'||new TextEncoder().encode(source).length>exportLimits.pageBytes)throw new Error('Export response exceeds the page byte limit.')
 let index=0,nodes=0
 const fail=()=>{throw new Error('Cannot decode bounded JSON data.')}
 const space=()=>{while(/[\x20\t\r\n]/.test(source[index]??'!'))index++}
 const string=()=>{
  const start=index++;while(index<source.length){const character=source[index++];if(character==='\\'){index++;continue}if(character==='"'){try{return JSON.parse(source.slice(start,index))}catch{fail()}}}fail()
 }
 function value(depth){
  space();if(++nodes>exportLimits.nodes||depth>exportLimits.depth)fail()
  const character=source[index]
  if(character==='"')return string()
  if(character==='['||character==='{'){
   const array=character==='[',result=array?[]:{},end=array?']':'}';index++;space()
   if(source[index]===end){index++;return result}
   for(;;){
    if(array)result.push(value(depth+1))
    else{space();if(source[index]!=='"')fail();const key=string();space();if(source[index++]!==':')fail();Object.defineProperty(result,key,{value:value(depth+1),enumerable:true,writable:true,configurable:true})}
    space();if(source[index]===end){index++;return result}if(source[index++]!==',')fail()
   }
  }
  for(const [literal,result] of [['true',true],['false',false],['null',null]])if(source.startsWith(literal,index)){index+=literal.length;return result}
  const match=source.slice(index).match(numberPattern);if(!match)fail();index+=match[0].length;return new ExactNumber(match[0])
 }
 const result=value(0);space();if(index!==source.length)fail();return result
}
export function exactJSON(value,depth=0){
 if(depth>exportLimits.depth)throw new Error('Export value exceeds nesting bounds.')
 if(value instanceof ExactNumber)return value.source
 if(value===null||typeof value==='string'||typeof value==='boolean')return JSON.stringify(value)
 if(Array.isArray(value))return '['+value.map(item=>exactJSON(item,depth+1)).join(',')+']'
 if(value&&typeof value==='object')return '{'+Object.entries(value).map(([key,item])=>JSON.stringify(key)+':'+exactJSON(item,depth+1)).join(',')+'}'
 throw new Error('Export value is not supported JSON.')
}
function normalizedNumber(source){
 const [mantissa,exponent='0']=source.toLowerCase().split('e'),negative=mantissa.startsWith('-'),unsigned=negative?mantissa.slice(1):mantissa,[whole,fraction='']=unsigned.split('.')
 let digits=(whole+fraction).replace(/^0+/,'');if(!digits)return '0'
 let power=BigInt(exponent)-BigInt(fraction.length)
 const trailing=digits.match(/0+$/)?.[0].length??0;if(trailing){digits=digits.slice(0,-trailing);power+=BigInt(trailing)}
 return `${negative?'-':''}${digits}e${power}`
}
export function decodeJsonCell(source){
 const parsed=parseExactJSON(source)
 const visit=value=>{
  if(value instanceof ExactNumber){const number=Number(value.source);if(!Number.isFinite(number)||normalizedNumber(value.source)!==normalizedNumber(JSON.stringify(number)))throw new Error('precision_loss');return number}
  if(Array.isArray(value))return value.map(visit)
  if(value&&typeof value==='object')return Object.fromEntries(Object.entries(value).map(([key,item])=>[key,visit(item)]))
  return value
 }
 return visit(parsed)
}
const csv=value=>'"'+value.replaceAll('"','""')+'"'
const safe=value=>/^[\t\r\n]/.test(value)||/^[\s\uFEFF]*[=+\-@]/.test(value)?"'"+value:value
export function exportHeader(columns,mode){return (mode==='spreadsheet'?'\uFEFF':'')+columns.map(column=>csv(mode==='spreadsheet'?safe(column):column)).join(',')+'\r\n'}
export function exportRecord(row,columns,mode){
 return columns.map(column=>{
  if(!Object.hasOwn(row,column))throw new Error('Export response omitted a selected column.')
  const value=row[column]
  if(mode==='json')return csv(exactJSON(value))
  const text=value===null?'':typeof value==='string'?safe(value):value instanceof ExactNumber?value.source:typeof value==='boolean'?String(value):exactJSON(value)
  return csv(text)
 }).join(',')+'\r\n'
}
export function csvExportPlan({table,columns,filter=null,scope=null,sort=null,direction='asc',fingerprint,mode='spreadsheet',rowLimit=1000}){
 const fail=()=>{throw new Error('Choose known columns, supported filters/sorting, an output mode and a row limit from 1 to 10,000.')}
 if(!table||!Array.isArray(columns)||!columns.length||columns.length>exportLimits.columns||new Set(columns).size!==columns.length||columns.some(name=>!table.columns.some(column=>column.name===name))||!['spreadsheet','json'].includes(mode)||!Number.isInteger(rowLimit)||rowLimit<1||rowLimit>exportLimits.rows||!/^[a-f0-9]{64}$/.test(fingerprint))fail()
 const known=name=>simple(name)&&table.columns.some(column=>column.name===name)
 if(filter&&(!known(filter.column)||!['eq','neq','gt','gte','lt','lte','is','ilike'].includes(filter.operator)||typeof filter.value!=='string'||filter.value.length>4096))fail()
 const predicates=[]
 for(const entry of scope?.predicates??[]){if(!Array.isArray(entry)||entry.length!==2||!known(entry[0])||typeof entry[1]!=='string'||!entry[1].startsWith('eq.'))fail();predicates.push([...entry])}
 if(scope&&(scope.schema!==table.schema||scope.table!==table.name||!predicates.length))fail()
 const order=[]
 if(sort){if(!known(sort)||!['asc','desc'].includes(direction))fail();order.push([sort,direction])}
 for(const name of table.primary_key?.columns??[])if(table.columns.some(column=>column.name===name)&&!order.some(entry=>entry[0]===name))order.push([name,'asc'])
 if(!order.length){const column=table.columns.find(column=>known(column.name)&&['string','number','boolean'].includes(column.type));if(!column)throw new Error('Choose a sortable column before exporting this relation.');order.push([column.name,'asc'])}
 return Object.freeze({schema:table.schema,table:table.name,columns:Object.freeze([...columns]),filter:filter?Object.freeze({...filter}):null,predicates:Object.freeze(predicates.map(entry=>Object.freeze(entry))),order:Object.freeze(order.map(entry=>Object.freeze(entry))),fingerprint,mode,rowLimit})
}
export function exportTarget(plan,origin,offset,limit){
 const target=new URL(`/__gregale/contract/${plan.fingerprint}/rest/v1/${encodeURIComponent(plan.table)}`,origin)
 target.searchParams.set('select',plan.columns.map(name=>JSON.stringify(name)).join(','));target.searchParams.set('order',plan.order.map(([name,direction])=>`${JSON.stringify(name)}.${direction}`).join(','));target.searchParams.set('offset',String(offset));target.searchParams.set('limit',String(limit))
 if(plan.filter)target.searchParams.append(plan.filter.column,`${plan.filter.operator}.${plan.filter.value}`)
 for(const [name,value] of plan.predicates)target.searchParams.append(name,value)
 return target
}
export async function runCsvExport(plan,{fetchPage,current=()=>true,signal,onProgress=()=>{}}){
 const chunks=[exportHeader(plan.columns,plan.mode)];let bytes=new TextEncoder().encode(chunks[0]).length,rows=0,limited=false,pages=0
 for(;;){
  signal?.throwIfAborted();if(!current())throw new Error('Export context changed. Start a fresh export.')
  if(++pages>exportLimits.rows+1)throw new Error('Export exceeds the page bound.')
  const limit=Math.min(exportLimits.pageRows,plan.rowLimit-rows+1),page=await fetchPage(rows,limit)
  signal?.throwIfAborted();if(!current())throw new Error('Export context changed. Start a fresh export.')
  if(!Array.isArray(page)||page.length>limit||page.some(row=>!row||typeof row!=='object'||Array.isArray(row)||row instanceof ExactNumber))throw new Error('Unexpected export row response.')
  for(const row of page){if(rows===plan.rowLimit){limited=true;break}const encoded=exportRecord(row,plan.columns,plan.mode);bytes+=new TextEncoder().encode(encoded).length;if(bytes>exportLimits.bytes)throw new Error('Export exceeds 10 MiB; use a smaller row limit or narrower filters.');chunks.push(encoded);rows++}
  onProgress({rows,bytes,limited})
  if(limited||page.length===0)break
 }
 return {csv:chunks.join(''),rows,bytes,limited,mode:plan.mode}
}
