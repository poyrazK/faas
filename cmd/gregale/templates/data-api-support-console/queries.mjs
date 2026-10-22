import {validateView} from './public/views.mjs'
const identifier=value=>typeof value==='string'&&/^[A-Za-z_][A-Za-z0-9_]*$/.test(value)&&!['select','order','and','or','not','limit','offset'].includes(value)
export function validateConsole(value){
 if(value?.version!==1||!Array.isArray(value.resources)||!value.resources.length||value.resources.length>10)throw new Error('Configure 1–10 approved resources.')
 const ids=new Set()
 for(const r of value.resources){
  if(!identifier(r.id)||ids.has(r.id)||!identifier(r.table)||typeof r.label!=='string'||r.label.length>100||!Array.isArray(r.columns)||!r.columns.length||r.columns.length>20||!r.columns.every(identifier)||new Set(r.columns).size!==r.columns.length||!Array.isArray(r.keys)||!r.keys.length||r.keys.length>16||new Set(r.keys).size!==r.keys.length||!r.keys.every(key=>r.columns.includes(key))||!r.columns.includes(r.search)||r.filter&&(!r.columns.includes(r.filter.column)||!Array.isArray(r.filter.values)||r.filter.values.length>20||r.filter.values.some(value=>typeof value!=='string'||value.length>100)))throw new Error('Configure safe columns, keys, search and filters.')
  if(r.labels&&(typeof r.labels!=='object'||Array.isArray(r.labels)||Object.entries(r.labels).some(([key,value])=>!r.columns.includes(key)||typeof value!=='string'||!value||value.length>100)))throw new Error('Configure labels for approved columns only.')
  if(r.staffOnly!==undefined&&typeof r.staffOnly!=="boolean")throw new Error("Configure boolean staff-only resource markers.")
  ids.add(r.id)
 }
 for(const r of value.resources){if(!Array.isArray(r.relationships)||r.relationships.length>10)throw new Error('Configure bounded relationships.');const names=new Set();for(const rel of r.relationships){const target=value.resources.find(item=>item.id===rel.target);if(!identifier(rel.id)||names.has(rel.id)||!target||!Array.isArray(rel.from)||!rel.from.length||rel.from.length>16||rel.from.length!==rel.to?.length||!rel.from.every(name=>r.columns.includes(name))||!rel.to.every(name=>target.columns.includes(name)))throw new Error('Configure complete approved relationships.');names.add(rel.id)}}
 if(value.views!==undefined){if(!Array.isArray(value.views)||value.views.length>20)throw new Error('Configure at most 20 shared views.');const names=new Set();for(const view of value.views){const approved=validateView(view,value);if(names.has(approved.name))throw new Error('Use unique shared view names.');names.add(approved.name)}}
 return value
}
export function dataTarget(config,definition,params){
 const fail=()=>{throw new Error('Choose an approved resource and supported query.')}
 for(const name of params.keys())if(!['resource','search','filter','sort','direction','offset','limit','scope'].includes(name)||params.getAll(name).length!==1)fail()
 const resource=definition.resources.find(item=>item.id===params.get('resource'));if(!resource)fail()
 const offset=Number(params.get('offset')??0),limit=Number(params.get('limit')??51),sort=params.get('sort')??resource.keys[0],direction=params.get('direction')??'asc',search=params.get('search')??'',filter=params.get('filter')??''
 if(!Number.isInteger(offset)||offset<0||offset>10000||!Number.isInteger(limit)||limit<1||limit>100||!resource.columns.includes(sort)||!['asc','desc'].includes(direction)||search.length>128||/[\x00-\x1f\x7f]/.test(search)||filter&&(!resource.filter||!resource.filter.values.includes(filter)))fail()
 const target=new URL(`/__gregale/contract/${config.fingerprint}/rest/v1/${encodeURIComponent(resource.table)}`,config.api)
 target.searchParams.set('select',resource.columns.map(name=>JSON.stringify(name)).join(','));target.searchParams.set('limit',String(limit));target.searchParams.set('offset',String(offset))
 target.searchParams.set('order',[[sort,direction],...resource.keys.filter(key=>key!==sort).map(key=>[key,'asc'])].map(([name,dir])=>`${JSON.stringify(name)}.${dir}`).join(','))
 if(search)target.searchParams.append(resource.search,'ilike.*'+search.replace(/[\\%_*]/g,'\\$&')+'*')
 if(filter)target.searchParams.append(resource.filter.column,'eq.'+filter)
 if(params.has('scope')){
  let scope;try{scope=JSON.parse(params.get('scope'))}catch{fail()}
  const source=definition.resources.find(item=>item.id===scope?.source),relation=source?.relationships.find(item=>item.id===scope?.relationship&&item.target===resource.id)
  if(!relation||!Array.isArray(scope.values)||scope.values.length!==relation.to.length||scope.values.some(value=>!['string','number','boolean'].includes(typeof value)||typeof value==='string'&&value.length>256||typeof value==='number'&&!Number.isSafeInteger(value)))fail()
  relation.to.forEach((name,index)=>target.searchParams.append(name,'eq.'+String(scope.values[index])))
 }
 return {target,resource,limit}
}
