import http from 'node:http'
import {randomBytes,createHash} from 'node:crypto'
import {generateKeyPair,exportJWK,SignJWT,jwtVerify,createLocalJWKSet} from 'jose'
import {readFile} from 'node:fs/promises'
import {pathToFileURL} from 'node:url'
import {settings} from './auth.mjs'
import {createConsole} from './server.mjs'
export const listen=(server,port=0)=>new Promise(resolve=>server.listen(port,'127.0.0.1',resolve))
export async function demoProvider({port=0,readOnly=true,nonceOverride=null}={}){
 const {privateKey,publicKey}=await generateKeyPair('ES256'),jwk={...await exportJWK(publicKey),kid:'demo-support',alg:'ES256'},codes=new Map(),keys=createLocalJWKSet({keys:[jwk]})
 let issuer
 const sign=claims=>new SignJWT(claims).setProtectedHeader({alg:'ES256',kid:jwk.kid}).setIssuer(issuer).setIssuedAt().setExpirationTime('10m').sign(privateKey)
 const server=http.createServer((req,res)=>{const send=(status,body,type='application/json')=>{res.writeHead(status,{'Content-Type':type,'Cache-Control':'no-store'});res.end(typeof body==='string'?body:JSON.stringify(body))};(async()=>{
  const url=new URL(req.url,issuer)
  if(url.pathname==='/.well-known/openid-configuration')return send(200,{issuer,authorization_endpoint:issuer+'/authorize',token_endpoint:issuer+'/token',jwks_uri:issuer+'/jwks',response_types_supported:['code'],code_challenge_methods_supported:['S256'],token_endpoint_auth_methods_supported:['none']})
  if(url.pathname==='/jwks')return send(200,{keys:[jwk]})
  if(url.pathname==='/authorize'){
   if(url.searchParams.get('client_id')!=='support-console-demo'||url.searchParams.get('code_challenge_method')!=='S256')return send(400,{error:'invalid_request'})
   const callback=new URL(url.searchParams.get('redirect_uri'));if(callback.hostname!=='127.0.0.1'||callback.pathname!=='/auth/callback')return send(400,{error:'invalid_request'})
   if(!url.searchParams.has('agent')){const links=['a','b'].map(agent=>{const next=new URL(url);next.searchParams.set('agent',agent);return '<p><a href="'+next.pathname+next.search.replaceAll('&','&amp;')+'">Continue as support agent '+agent.toUpperCase()+'</a></p>'}).join('');return send(200,'<!doctype html><html lang="en"><title>Demo staff sign-in</title><h1>Demo staff sign-in</h1><p>Synthetic identities and customer records only.</p>'+links+'</html>','text/html')}
   const agent=url.searchParams.get('agent');if(!['a','b'].includes(agent))return send(400,{error:'invalid_request'})
   const code=randomBytes(24).toString('base64url');codes.set(code,{agent,challenge:url.searchParams.get('code_challenge'),nonce:url.searchParams.get('nonce'),callback:callback.toString(),expires:Date.now()+60000})
   callback.searchParams.set('code',code);callback.searchParams.set('state',url.searchParams.get('state'));res.writeHead(303,{Location:callback.toString()});return res.end()
  }
  if(url.pathname==='/token'&&req.method==='POST'){
   let body='';for await(const chunk of req){body+=chunk;if(body.length>8192)return send(400,{error:'invalid_request'})}
   const params=new URLSearchParams(body),grant=codes.get(params.get('code'));codes.delete(params.get('code'))
   if(!grant||grant.expires<Date.now()||grant.challenge!==createHash('sha256').update(params.get('code_verifier')??'').digest('base64url')||params.get('redirect_uri')!==grant.callback||params.get('client_id')!=='support-console-demo')return send(400,{error:'invalid_grant'})
   const subject='agent-'+grant.agent
   return send(200,{token_type:'Bearer',access_token:await sign({sub:subject,name:'Support agent '+grant.agent.toUpperCase(),aud:'support-data-demo',tenant:'shop-'+grant.agent,gregale_read_only:readOnly,gregale_support_actions:grant.agent==='a'?['add_reply','close_case','assign_case','unassign_case','set_priority','add_note']:[],gregale_internal_notes:grant.agent==='a'}),id_token:await sign({sub:subject,aud:'support-console-demo',nonce:nonceOverride??grant.nonce})})
  }
  send(404,{error:'not_found'})
 })().catch(()=>send(400,{error:'invalid_request'}))})
 await listen(server,port);issuer='http://127.0.0.1:'+server.address().port
 return {server,issuer,verify:async token=>(await jwtVerify(token,keys,{issuer,audience:'support-data-demo',algorithms:['ES256']})).payload,sign}
}
export async function demoData(provider,port=0){
 const customers=[{tenant:'shop-a',id:1,name:'Alex Customer',email:'alex@example.test'},{tenant:'shop-a',id:3,name:'Alex Duplicate',email:'alex@example.test'},{tenant:'shop-b',id:2,name:'Other Customer',email:'alex@example.test'}]
 const calls=[],cases=Array.from({length:120},(_,index)=>({tenant:'shop-a',id:index+1,title:index===0?'=Demo case 1':'Demo case '+(index+1),status:index%2?'closed':'open',internal_notes:'private demo value'}));cases.push({tenant:'shop-b',id:121,title:'Other team case',status:'open',internal_notes:'private other value'})
 const assignmentEvents=[],priorityEvents=[],notes=[]
 const replies=[{tenant:'shop-a',id:1,case_id:1,body:'Customer requested delivery details.',internal_notes:'private reply'},{tenant:'shop-a',id:2,case_id:1,body:'Tracking information shared.',internal_notes:'private reply'},{tenant:'shop-b',id:3,case_id:121,body:'Other team conversation.',internal_notes:'private reply'}]
 for(const row of cases){row.version=1;row.assignee=null;row.priority='normal';row.created_at=new Date().toISOString();row.first_response_at=row.id===1?new Date().toISOString():null;row.response_due_at=new Date(Date.now()+86400000).toISOString()}
 cases.find(r=>r.id===5).response_due_at=new Date(Date.now()-60000).toISOString();cases.find(r=>r.id===7).response_due_at=new Date(Date.now()+1800000).toISOString()
 for(const row of [...cases,...replies])row.customer_id=row.tenant==='shop-a'?1:2
 for(const row of [...cases,...replies])row.occurred_at='2026-01-01T00:00:00.000Z'
 const server=http.createServer((req,res)=>{const send=(status,body)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(body))};(async()=>{
  let claims;try{claims=await provider.verify(req.headers.authorization?.slice(7))}catch{return send(401,{error:'token_invalid'})}
  if(claims.gregale_read_only!==true||req.method!=='GET')return send(403,{error:'read_only_identity'})
  const url=new URL(req.url,'http://localhost'),table=url.pathname.split('/').at(-1);if(!['support_cases','support_replies','support_customers','support_assignment_events','support_priority_events','support_internal_notes'].includes(table))return send(404,{error:'not_found'})
  if(table==='support_internal_notes'&&claims.gregale_internal_notes!==true)return send(403,{error:'staff_only'})
  calls.push({method:req.method,table,tenant:claims.tenant,query:new URLSearchParams(url.search)})
  let rows=(table==='support_cases'?cases:table==='support_customers'?customers:table==='support_assignment_events'?assignmentEvents:table==='support_priority_events'?priorityEvents:table==='support_internal_notes'?notes:replies).filter(row=>row.tenant===claims.tenant)
  for(const [key,value]of url.searchParams){if(/^(lt|lte|gte)\./.test(value)){const [op,...rest]=value.split('.'),bound=Date.parse(rest.join('.'));rows=rows.filter(row=>row[key]!==null&&(op==='lt'?Date.parse(row[key])<bound:op==='lte'?Date.parse(row[key])<=bound:Date.parse(row[key])>=bound))}if(value==='is.null')rows=rows.filter(row=>row[key]===null);if(value.startsWith('eq.'))rows=rows.filter(row=>String(row[key])===value.slice(3));if(value.startsWith('ilike.')){const needle=value.slice(6).replace(/^\*|\*$/g,'').replace(/\\([\\%_*])/g,'$1').toLowerCase();rows=rows.filter(row=>String(row[key]).toLowerCase().includes(needle))}}
  const order=url.searchParams.get('order')?.split(',')??[];rows=[...rows].sort((a,b)=>{for(const entry of order){const [quoted,direction]=entry.split('.'),key=JSON.parse(quoted);if(a[key]!==b[key])return (a[key]<b[key]?-1:1)*(direction==='desc'?-1:1)}return 0})
  const offset=Number(url.searchParams.get('offset')??0),limit=Number(url.searchParams.get('limit')??51)
  // Extra properties deliberately exercise the console's independent projection allowlist.
  send(200,rows.slice(offset,offset+limit))
 })().catch(()=>send(400,{error:'invalid_request'}))})
 await listen(server,port);return {server,url:'http://127.0.0.1:'+server.address().port,calls,cases,replies,customers,assignmentEvents,priorityEvents,notes}
}
export async function startDemo({port=3030,providerOptions={}}={}){
 const provider=await demoProvider(providerOptions),data=await demoData(provider),definition=JSON.parse(await readFile(new URL('./support-console.json',import.meta.url)))
 for(const r of definition.resources)r.columns.push('occurred_at','customer_id')
 definition.resources.push({id:'customers',label:'Customers',table:'support_customers',columns:['tenant','id','name','email'],keys:['tenant','id'],search:'name',relationships:[{id:'cases',label:'Cases',target:'cases',from:['tenant','id'],to:['tenant','customer_id']},{id:'replies',label:'Replies',target:'replies',from:['tenant','id'],to:['tenant','customer_id']}]})
 definition.resources.find(r=>r.id==='cases').columns.push('version','assignee','priority','created_at','first_response_at','response_due_at')
 definition.actions=[{id:'add_reply',label:'Add reply',resource:'cases',idColumn:'id',version:'version'},{id:'close_case',label:'Close case',resource:'cases',idColumn:'id',version:'version'}]
 definition.actions.push({id:'assign_case',label:'Assign to me',resource:'cases',idColumn:'id',version:'version'},{id:'unassign_case',label:'Unassign case',resource:'cases',idColumn:'id',version:'version'})
 definition.actions.push({id:'set_priority',label:'Change priority',resource:'cases',idColumn:'id',version:'version'})
 definition.queues={resource:'cases',assignee:'assignee',status:'status',deadline:'response_due_at',firstResponse:'first_response_at',dueSoonMinutes:60}
 definition.resources.push({id:'priorities',label:'Priority history',table:'support_priority_events',columns:['id','tenant','case_id','actor','previous_priority','priority','title','occurred_at'],keys:['tenant','id'],search:'title',relationships:[]})
 definition.resources.push({id:'assignments',label:'Assignment history',table:'support_assignment_events',columns:['id','tenant','case_id','actor','previous_assignee','assignee','title','occurred_at'],keys:['tenant','id'],search:'title',relationships:[]})
 definition.actions.push({id:'add_note',label:'Add internal note',resource:'cases',idColumn:'id',version:'version'})
 definition.resources.push({id:'notes',label:'Internal notes (staff only)',table:'support_internal_notes',staffOnly:true,columns:['id','tenant','case_id','actor','body','occurred_at'],keys:['tenant','id'],search:'body',relationships:[]})
 definition.customer={resource:'customers',title:'name',lookup:['email','id'],relationships:['cases','replies']}
 definition.timelines=[{id:'case_history',label:'Case timeline',resource:'cases',from:['tenant','id'],sources:[{resource:'cases',to:['tenant','id'],timestamp:'occurred_at',title:'title'},{resource:'replies',to:['tenant','case_id'],timestamp:'occurred_at',title:'body'}]}]
 definition.timelines[0].label='Case activity'
 definition.timelines[0].sources.push({resource:'notes',to:['tenant','case_id'],timestamp:'occurred_at',title:'body'})
 definition.timelines[0].sources.push({resource:'priorities',to:['tenant','case_id'],timestamp:'occurred_at',title:'title'})
 definition.timelines[0].sources.push({resource:'assignments',to:['tenant','case_id'],timestamp:'occurred_at',title:'title'})
 definition.timelines.push({id:'customer_history',label:'Customer timeline',resource:'customers',from:['tenant','id'],sources:[{resource:'replies',to:['tenant','customer_id'],timestamp:'occurred_at',title:'body'}]});definition.customer.timeline='customer_history'
 // Reserve the console port first when tests request an ephemeral origin.
 let actualPort=port;if(!port){const reserve=http.createServer();await listen(reserve);actualPort=reserve.address().port;await new Promise(resolve=>reserve.close(resolve))}
 const config=settings({SUPPORT_ORIGIN:'http://127.0.0.1:'+actualPort,SUPPORT_OIDC_ISSUER:provider.issuer,SUPPORT_OIDC_CLIENT_ID:'support-console-demo',SUPPORT_DATA_AUDIENCE:'support-data-demo',SUPPORT_DATA_API_URL:data.url,SUPPORT_SCHEMA_FINGERPRINT:'a'.repeat(64),SUPPORT_SESSION_KEY:randomBytes(32).toString('base64url')})
 config.audit.admins=[{subject:"agent-a",tenant:"shop-a"}]
 const auditEvents=[]
 const receipts=new Map(),requests=new Map()
 const actionExecutor={
 async register(claims,request){requests.set(request.id,{id:request.id,action:request.action,time:new Date().toISOString(),state:'previewed',subject:claims.sub,tenant:claims.tenant})},
 async recent(claims,id=null){return [...requests.values()].filter(r=>r.subject===claims.sub&&r.tenant===claims.tenant&&(!id||r.id===id)).reverse().slice(0,50)},
 async execute(claims,request){
 const entry=requests.get(request.id);entry.state='unresolved'

  const prior=receipts.get(request.id);if(prior){entry.state="completed";entry.result=prior;return prior}
  if(request.reviewExpires<Date.now()){entry.state="rejected";throw Object.assign(new Error("Confirmation expired. Preview again."),{status:409})}
  const row=data.cases.find(row=>row.tenant===claims.tenant&&row.id===request.caseId)
  if(!row||row.status!=='open'||row.version!==request.version){entry.state='rejected';throw Object.assign(new Error('Case changed. Refresh and preview again.'),{status:409})}
  const result={action:request.action,version:++row.version,status:request.action==='close_case'?'closed':'open'}
  if(request.action==='close_case')row.status='closed'
  else if(request.action==='add_note'){const id=data.notes.length+1;data.notes.push({id,tenant:row.tenant,case_id:row.id,actor:claims.sub,body:request.body,occurred_at:new Date().toISOString()});result.note_id=id}
  else if(request.action==='set_priority'){const prior=row.priority;row.priority=request.body;if(!row.first_response_at)row.response_due_at=new Date(Date.parse(row.created_at)+({low:2880,normal:1440,high:240,urgent:60}[row.priority])*60000).toISOString();result.priority=row.priority;result.response_due_at=row.response_due_at;data.priorityEvents.push({id:data.priorityEvents.length+1,tenant:row.tenant,case_id:row.id,actor:claims.sub,previous_priority:prior,priority:row.priority,title:'Priority changed to '+row.priority,occurred_at:new Date().toISOString()})}
  else if(['assign_case','unassign_case'].includes(request.action)){const prior=row.assignee;row.assignee=request.action==='assign_case'?claims.sub:null;result.assignee=row.assignee;data.assignmentEvents.push({id:data.assignmentEvents.length+1,tenant:row.tenant,case_id:row.id,actor:claims.sub,previous_assignee:prior,assignee:row.assignee,title:row.assignee?'Case assigned':'Case unassigned',occurred_at:new Date().toISOString()})}
  else{row.first_response_at??=new Date().toISOString();const id=Math.max(...data.replies.map(r=>r.id))+1;data.replies.push({tenant:row.tenant,id,case_id:row.id,customer_id:row.customer_id,body:request.body,occurred_at:new Date().toISOString(),internal_notes:''});result.reply_id=id}
  receipts.set(request.id,result);entry.state="completed";entry.result=result;return result
 }}
 const server=await createConsole(config,definition,{auditSink:event=>auditEvents.push(event),actionExecutor});await listen(server,actualPort)
 const close=async()=>{for(const service of [server,data.server,provider.server]){service.closeAllConnections();await new Promise(resolve=>service.close(resolve))}}
 return {server,provider,data,config,definition,url:config.origin,close,auditEvents}
}
if(process.argv[1]&&pathToFileURL(process.argv[1]).href===import.meta.url){const demo=await startDemo({port:Number(process.env.DEMO_PORT??3030)});console.log('Synthetic support console demo: '+demo.url);for(const signal of ['SIGINT','SIGTERM'])process.once(signal,()=>demo.close().then(()=>process.exit(0)))}
