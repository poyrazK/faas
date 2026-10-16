export function validateView(value,definition){
 const allowed=['name','resource','search','filter','sort','direction','queue']
 if(!value||typeof value!=='object'||Array.isArray(value)||Object.keys(value).some(key=>!allowed.includes(key)))throw new Error('Unsupported saved view.')
 const r=definition.resources.find(r=>r.id===value.resource)
 if(!r||typeof value.name!=='string'||!value.name.trim()||value.name.length>80||typeof value.search!=='string'||value.search.length>128||/[\x00-\x1f\x7f]/.test(value.search)||typeof value.filter!=='string'||value.filter&& !r.filter?.values.includes(value.filter)||!r.columns.includes(value.sort)||!['asc','desc'].includes(value.direction))throw new Error('Saved view no longer matches approved resources.')
 if(value.queue!==undefined&&(!['mine','unassigned','due_soon','overdue'].includes(value.queue)||definition.queues?.resource!==r.id||['due_soon','overdue'].includes(value.queue)&&!definition.queues?.deadline))throw new Error('Unsupported support queue.')
 return Object.fromEntries(allowed.filter(key=>key!=='queue'||value.queue!==undefined).map(key=>[key,key==='name'?value.name.trim():value[key]]))
}
export function viewLink(origin,view,definition){
 const url=new URL('/',origin);url.hash=new URLSearchParams({view:JSON.stringify(validateView(view,definition))}).toString();return url.href
}
export function linkedView(hash,definition){
 if(!hash)return null
 if(hash.length>3000)throw new Error('Saved view link is too large.')
 const params=new URLSearchParams(hash.slice(1))
 if([...params.keys()].some(key=>key!=='view')||params.getAll('view').length!==1)throw new Error('Unsupported saved view link.')
 return validateView(JSON.parse(params.get('view')),definition)
}
export function preferenceKey(staff){return 'gregale.support.views.v1:'+JSON.stringify([staff.subject,staff.tenant])}
export function loadViews(storage,key,definition){
 try{const raw=storage.getItem(key);if(!raw)return [];if(raw.length>40000)return [];const views=JSON.parse(raw);if(!Array.isArray(views)||views.length>20)return [];return views.map(view=>validateView(view,definition))}catch{return []}
}
