import {dataTarget} from './queries.mjs'
import {exactJSON,decodeJsonCell} from './public/csv.mjs'
export function validateTimelines(definition){
 const ids=new Set()
 if(definition.timelines===undefined)return
 if(!Array.isArray(definition.timelines)||definition.timelines.length>10)throw new Error('Configure at most ten timelines.')
 for(const t of definition.timelines){
  const anchor=definition.resources.find(r=>r.id===t.resource)
  if(!anchor||typeof t.id!=='string'||!/^[a-z][a-z0-9_]*$/.test(t.id)||ids.has(t.id)||typeof t.label!=='string'||!t.label||t.label.length>80||!Array.isArray(t.from)||!t.from.length||t.from.length>16||new Set(t.from).size!==t.from.length||!t.from.every(c=>anchor.columns.includes(c))||!Array.isArray(t.sources)||!t.sources.length||t.sources.length>10)throw new Error('Configure approved timeline anchors and sources.')
  ids.add(t.id);const resources=new Set()
  for(const source of t.sources){
   const r=definition.resources.find(r=>r.id===source.resource)
   if(!r||resources.has(r.id)||!r.columns.includes(source.timestamp)||!r.columns.includes(source.title)||!Array.isArray(source.to)||source.to.length!==t.from.length||new Set(source.to).size!==source.to.length||!source.to.every(c=>r.columns.includes(c)))throw new Error('Configure complete timeline mappings and approved event fields.')
   resources.add(r.id)
  }
 }
}
export function timelineRequest(definition,params){
 for(const key of params.keys())if(!['timeline','values','snapshot','offset'].includes(key)||params.getAll(key).length!==1)throw new Error('Unsupported timeline query.')
 const t=definition.timelines?.find(t=>t.id===params.get('timeline'));if(!t)throw new Error('Unknown timeline.')
 const values=JSON.parse(params.get('values')??'null'),offset=Number(params.get('offset')??0)
 if(!Array.isArray(values)||values.length!==t.from.length||values.some(v=>!['string','number','boolean'].includes(typeof v)||typeof v==='string'&&(v.length>256||/[\x00-\x1f\x7f]/.test(v))||typeof v==='number'&&!Number.isSafeInteger(v))||!Number.isInteger(offset)||offset<0||offset>1000||offset%25)throw new Error('Invalid timeline scope or page.')
 return {t,values,offset,snapshot:params.get('snapshot')}
}
export function sourceQuery(config,definition,source,values){
 const query=dataTarget(config,definition,new URLSearchParams({resource:source.resource,sort:source.timestamp,direction:'desc',limit:'100'}))
 query.limit=101;query.target.searchParams.set('limit','101')
 source.to.forEach((column,index)=>query.target.searchParams.append(column,'eq.'+String(values[index])))
 return query
}
export function eventsFor(source,resource,rows){
 if(rows.length>100)throw Object.assign(new Error('Timeline source exceeds 100 events. Ask your administrator to narrow the customer mapping.'),{status:422})
 return rows.map(row=>{
  const time=row[source.timestamp]
  if(typeof time!=='string'||! /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,3})?(?:Z|[+-]\d\d:\d\d)$/.test(time)||!Number.isFinite(Date.parse(time)))throw new Error('Timeline timestamps must be non-null ISO timestamps.')
  const keys=resource.keys.map(k=>decodeJsonCell(exactJSON(row[k])))
  if(keys.some(v=>!['string','number','boolean'].includes(typeof v)||typeof v==='number'&&!Number.isSafeInteger(v)))throw new Error('Timeline requires scalar safe keys.')
  return {id:JSON.stringify([resource.id,...keys]),time,title:typeof row[source.title]==='string'?row[source.title]:exactJSON(row[source.title]),resource:resource.id,label:resource.label,row}
 })
}
export function orderEvents(events){
 const ids=new Set()
 for(const event of events){if(ids.has(event.id))throw new Error('Duplicate timeline event key.');ids.add(event.id)}
 return events.sort((a,b)=>Date.parse(b.time)-Date.parse(a.time)||(a.id<b.id?-1:a.id>b.id?1:0))
}
