import {dataTarget} from './queries.mjs'
export function validateCustomers(definition){
 const c=definition.customer
 if(c===undefined)return
 const r=definition.resources.find(r=>r.id===c.resource)
 if(!r||!r.columns.includes(c.title)||!Array.isArray(c.lookup)||!c.lookup.length||c.lookup.length>10||new Set(c.lookup).size!==c.lookup.length||!c.lookup.every(column=>r.columns.includes(column))||!Array.isArray(c.relationships)||c.relationships.length>10||new Set(c.relationships).size!==c.relationships.length||!c.relationships.every(id=>r.relationships.some(rel=>rel.id===id))||c.timeline!==undefined&&!definition.timelines?.some(t=>t.id===c.timeline&&t.resource===r.id))throw new Error('Configure approved customer lookup, relationships and timeline.')
}
const scalars=values=>Array.isArray(values)&&values.every(v=>typeof v==='string'&&v.length<=256&&!/[\x00-\x1f\x7f]/.test(v)||typeof v==='number'&&Number.isSafeInteger(v)||typeof v==='boolean')
export function customerRequest(config,definition,params){
 const c=definition.customer,r=definition.resources.find(r=>r.id===c?.resource)
 if(!c||!r)throw new Error('Customer lookup is not configured.')
 for(const key of params.keys())if(!['field','value','keys','offset'].includes(key)||params.getAll(key).length!==1)throw new Error('Unsupported customer query.')
 const offset=Number(params.get('offset')??0)
 if(!Number.isInteger(offset)||offset<0||offset>10000||offset%25)throw new Error('Invalid lookup page.')
 const query=dataTarget(config,definition,new URLSearchParams({resource:r.id,limit:'26',offset:String(offset)}))
 if(params.has('keys')){
  if(params.has('field')||params.has('value')||offset)throw new Error('Choose lookup or profile.')
  const keys=JSON.parse(params.get('keys'));if(!scalars(keys)||keys.length!==r.keys.length)throw new Error('Invalid customer keys.')
  r.keys.forEach((column,i)=>query.target.searchParams.append(column,'eq.'+String(keys[i])))
  return {query,c,r,profile:true}
 }
 const field=params.get('field'),value=params.get('value')
 if(!c.lookup.includes(field)||typeof value!=='string'||!value.trim()||value.length>128||/[\x00-\x1f\x7f]/.test(value))throw new Error('Choose an approved lookup field and exact identifier.')
 query.target.searchParams.append(field,'eq.'+value)
 return {query,c,r,profile:false}
}
