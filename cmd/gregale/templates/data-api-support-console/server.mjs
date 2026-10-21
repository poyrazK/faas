import {validateActions,permissions,actionInput,previewQuery,sessionBinding,actionDatabase,actionResult} from './actions.mjs'
import {postgresAudit,auditFilters,auditCSV} from './audit-postgres.mjs'
import {auditLog,auditEvent} from './audit.mjs'
import {validateCustomers,customerRequest} from './customer.mjs'
import {validateTimelines,timelineRequest,sourceQuery,eventsFor,orderEvents} from './timeline.mjs'
import {parseExactJSON,exactJSON,decodeJsonCell} from './public/csv.mjs'
import http from 'node:http'
import {readFile} from 'node:fs/promises'
import {randomUUID,randomBytes,createHash,timingSafeEqual} from 'node:crypto'
import {pathToFileURL} from 'node:url'
import {settings,oidc,seal,unseal,cookie,cookies,boundedText} from './auth.mjs'
import {validateConsole,dataTarget} from './queries.mjs'
export async function createConsole(config,definition,{auditSink=event=>console.log(JSON.stringify({type:"support_audit",...event})),actionExecutor=null}={}){
 validateConsole(definition);validateTimelines(definition);validateCustomers(definition);validateActions(definition);if(definition.queues){const r=definition.resources.find(r=>r.id===definition.queues.resource);if(!r||!r.columns.includes(definition.queues.assignee)||!r.columns.includes(definition.queues.status))throw new Error("Configure approved queue columns.");if(definition.queues.deadline&&(!r.columns.includes(definition.queues.deadline)||!r.columns.includes(definition.queues.firstResponse)||!Number.isInteger(definition.queues.dueSoonMinutes)||definition.queues.dueSoonMinutes<1||definition.queues.dueSoonMinutes>1440))throw new Error("Configure approved response deadline columns and window.")}const provider=await oidc(config)
 const assets=new Map(await Promise.all(['index.html','app.mjs','style.css','csv.mjs','views.mjs'].map(async name=>[name,await readFile(new URL('public/'+name,import.meta.url))])))
 const actions=config.actionDatabase?await actionDatabase(config.actionDatabase):actionExecutor
 let durable;try{durable=config.audit?.url?await postgresAudit(config.audit):null}catch(error){if(config.actionDatabase)await actions.close();throw error}
 const audit=auditLog(config.audit,auditSink)
 const snapshots=new Map()
 const server=http.createServer((req,res)=>{
  let auditContext=null,audited=false
  const send=async(status,value,type='application/json')=>{if(auditContext&&!audited){audited=true;const event=auditEvent({...auditContext,status});if(durable&&!await durable.record(event)){status=503;value={error:"Audit delivery cannot be queued. Retry later."};type="application/json";event.status=503;event.outcome="failed"}audit.accept(event)}res.writeHead(status,{'Content-Type':type,'Cache-Control':'no-store','Referrer-Policy':'no-referrer','X-Content-Type-Options':'nosniff','Content-Security-Policy':"default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"});res.end(Buffer.isBuffer(value)?value:typeof value==='string'?value:JSON.stringify(value))}
  const signInError=status=>send(status,'<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Staff sign-in</title><link rel="stylesheet" href="/style.css"></head><body><main><section class="welcome"><h1>Sign-in could not be completed.</h1><p>Try again, or contact your administrator to check staff access.</p><a class="button" href="/auth/login">Try sign-in again</a></section></main></body></html>','text/html; charset=utf-8')
  const redirect=location=>{res.writeHead(303,{Location:location,'Cache-Control':'no-store','Referrer-Policy':'no-referrer'});res.end()}
  ;(async()=>{
   const url=new URL(req.url,config.origin),jar=cookies(req.headers.cookie)
   if(url.pathname==='/healthz'&&req.method==='GET')return send(200,{status:'ready'})
   if(url.pathname==='/auth/login'&&req.method==='GET'){
    const login={kind:'login',state:randomBytes(32).toString('base64url'),nonce:randomBytes(32).toString('base64url'),verifier:randomBytes(32).toString('base64url'),expires:Date.now()+300000}
    const target=new URL(provider.metadata.authorization_endpoint);target.searchParams.set('response_type','code');target.searchParams.set('client_id',config.clientId);target.searchParams.set('redirect_uri',config.origin+'/auth/callback');target.searchParams.set('scope',config.scopes);target.searchParams.set('state',login.state);target.searchParams.set('nonce',login.nonce);target.searchParams.set('code_challenge',createHash('sha256').update(login.verifier).digest('base64url'));target.searchParams.set('code_challenge_method','S256')
    if(config.audienceParameter){if(!['audience','resource'].includes(config.audienceParameter))throw new Error('Invalid audience parameter.');target.searchParams.set(config.audienceParameter,config.audience)}
    res.setHeader('Set-Cookie',cookie('support_login',seal(login,config.key),config,300));return redirect(target.toString())
   }
   if(url.pathname==='/auth/callback'&&req.method==='GET'){
    const login=unseal(jar.get('support_login'),config.key,'login'),state=url.searchParams.get('state')??'',code=url.searchParams.get('code')??''
    res.setHeader('Set-Cookie',cookie('support_login','',config,0))
    if(!login||!code||code.length>4096||state.length!==login.state.length||!timingSafeEqual(Buffer.from(state),Buffer.from(login.state)))return signInError(400)
    const body=new URLSearchParams({grant_type:'authorization_code',code,redirect_uri:config.origin+'/auth/callback',client_id:config.clientId,code_verifier:login.verifier}),headers={'Content-Type':'application/x-www-form-urlencoded'}
    if(config.clientSecret)headers.Authorization='Basic '+Buffer.from(encodeURIComponent(config.clientId)+':'+encodeURIComponent(config.clientSecret)).toString('base64')
    const response=await fetch(provider.metadata.token_endpoint,{method:'POST',headers,body,redirect:'error',signal:AbortSignal.timeout(10000)});const text=await boundedText(response,65536);if(!response.ok)throw new Error('Staff sign-in refused.')
    const tokens=JSON.parse(text);if(tokens.token_type?.toLowerCase()!=='bearer')throw new Error('Invalid access-token type.')
    const id=await provider.identity(tokens.id_token,login.nonce),access=await provider.access(tokens.access_token);if(id.sub!==access.sub)throw new Error('Staff subject mismatch.')
    const expires=Math.min(access.exp*1000,Date.now()+1800000),value=seal({kind:'session',token:tokens.access_token,expires},config.key);if(value.length>3500||expires<=Date.now())throw new Error('Invalid staff session size or expiry.')
    res.setHeader('Set-Cookie',[cookie('support_login','',config,0),cookie('support_session',value,config,Math.floor((expires-Date.now())/1000))]);return redirect('/')
   }
   if(req.method==='GET'&&['/','/app.mjs','/style.css','/csv.mjs','/views.mjs'].includes(url.pathname)){const name=url.pathname==='/'?'index.html':url.pathname.slice(1);return send(200,assets.get(name),name.endsWith('html')?'text/html; charset=utf-8':name.endsWith('css')?'text/css':'text/javascript')}
   if(url.pathname==='/auth/logout'&&req.method==='POST'){
    if(req.headers.origin!==config.origin||req.headers['sec-fetch-site']==='cross-site')return send(403,{error:'Request origin refused.'})
    res.setHeader('Set-Cookie',[cookie('support_session','',config,0),cookie('support_login','',config,0)]);return send(200,{signed_out:true})
   }
   if(!['/api/session','/api/config','/api/data','/api/timeline','/api/customer','/api/audit','/api/actions/preview','/api/actions/confirm','/api/actions/recent'].includes(url.pathname))return send(404,{error:'Not found.'})
   const actionRoute=url.pathname.startsWith('/api/actions/')&&!url.pathname.endsWith('/recent');if(req.method!==(actionRoute?'POST':'GET'))return send(405,{error:'This console permits reads only.'})
   if(req.headers['sec-fetch-site']==='cross-site'||req.headers.origin&&req.headers.origin!==config.origin)return send(403,{error:'Request origin refused.'})
   const session=unseal(jar.get('support_session'),config.key,'session');if(!session)return send(401,{error:'Sign in to continue.'})
   let claims;try{claims=await provider.access(session.token)}catch{res.setHeader('Set-Cookie',cookie('support_session','',config,0));return send(401,{error:'Sign in with a read-only staff identity.'})}
   const action=url.pathname==='/api/actions/recent'?'support.recovery':actionRoute?(url.pathname.endsWith('/preview')?'support.preview':'support.action'):url.pathname==='/api/customer'?(url.searchParams.has('keys')?'customer.profile':'customer.lookup'):url.pathname==='/api/timeline'?'customer.timeline':url.pathname==='/api/audit'?'audit.read':url.pathname==='/api/data'?(url.searchParams.get('purpose')==='export'?'data.export_page':'data.read'):null
   if(action)auditContext={claims,action}
   if(url.pathname==='/api/audit'){if(!audit.admin(claims))return send(403,{error:'Audit administrator access required.'});let filters;try{filters=auditFilters(url.searchParams)}catch{return send(400,{error:'Choose supported audit filters and UTC dates.'})}const exporting=url.searchParams.get('format')==='csv';if(exporting)auditContext.action='audit.export';let events;try{events=durable?await durable.read(claims,filters,exporting?1001:200):audit.read(claims,filters,exporting?1001:200)}catch{return send(503,{error:'Audit database unavailable. '+(durable?.status().pending??0)+' events queued; delivery retries continue.',delivery:durable?.status()})}if(exporting){if(events.length>1000)return send(422,{error:'More than 1000 audit events match. Narrow the filters.'});res.setHeader('Content-Disposition','attachment; filename="support-audit.csv"');return send(200,auditCSV(events),'text/csv; charset=utf-8')}return send(200,{events,delivery:durable?.status()??{mode:'memory',pending:0,last_failure:null},retention_days:config.audit?.days??7,max_events:durable?null:config.audit?.limit??1000,scope:'Current tenant; latest 200 matching events.'})}
   if(url.pathname==='/api/session')return send(200,{action_recovery:!!actions?.recent,actions:actions?permissions(claims,definition):[],audit_admin:audit.admin(claims),subject:claims.sub,staff:typeof claims.name==='string'&&claims.name.length<=100?claims.name:claims.sub,tenant:claims.tenant,expires:session.expires})
   if(url.pathname==='/api/config'){const allowed=r=>!r.staffOnly||claims.gregale_internal_notes===true;const resources=definition.resources.filter(allowed);return send(200,{...definition,views:definition.views?.filter(v=>resources.some(r=>r.id===v.resource)),resources:resources.map(r=>({...r,relationships:r.relationships.filter(rel=>resources.some(r=>r.id===rel.target))})),timelines:definition.timelines?.map(t=>({...t,sources:t.sources.filter(source=>resources.some(r=>r.id===source.resource))})),actions:definition.actions?.filter(a=>a.id!=='add_note'||claims.gregale_internal_notes===true)})}
   const readRows=async query=>{
   if(query.resource.staffOnly&&claims.gregale_internal_notes!==true)throw Object.assign(new Error('Staff-only notes access required.'),{status:403})
   const response=await fetch(query.target,{headers:{Authorization:`Bearer ${session.token}`,'Accept-Profile':'api',Accept:'application/json'},redirect:'error',signal:AbortSignal.timeout(15000)}),text=await boundedText(response,1048576)
   if(response.status===401)throw Object.assign(new Error('Your data session expired. Sign in again.'),{status:401})
   if(response.status===409)throw Object.assign(new Error('The data schema changed. Ask your administrator to update this console.'),{status:409})
   if(!response.ok)throw Object.assign(new Error('Data access refused. Review staff permissions and tenant access.'),{status:response.status===403?403:502})
   let rows;try{rows=parseExactJSON(text)}catch{throw new Error('Invalid data response.')}
   if(!Array.isArray(rows)||rows.length>query.limit||rows.some(row=>!row||typeof row!=='object'||Array.isArray(row)||query.resource.columns.some(column=>!Object.hasOwn(row,column))))throw new Error('Invalid data rows.')
   // Rebuild the approved projection, even if an upstream response contains extra properties.
   return rows.map(row=>Object.fromEntries(query.resource.columns.map(column=>[column,row[column]])))
   }
   if(url.pathname==='/api/actions/recent'){
    if(!actions?.recent)return send(403,{error:'Action recovery is not enabled.'})
    let id=null;for(const key of url.searchParams.keys())if(key!=='id'||url.searchParams.getAll(key).length!==1)return send(400,{error:'Unsupported action recovery query.'})
    if(url.searchParams.has('id')){id=url.searchParams.get('id');if(!/^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/.test(id))return send(400,{error:'Invalid action ID.'})}
    const records=await actions.recent(claims,id)
    return send(200,{actions:records.map(record=>({id:record.id,action:record.action,time:record.time,state:record.state,...(record.state==='completed'?{result:actionResult(record.result,record.action)}:{})}))})
   }
   if(actionRoute){
    if(req.headers.origin!==config.origin)return send(403,{error:'Request origin refused.'})
    if(!actions)return send(403,{error:'Support actions are not enabled.'})
    if(url.search||req.headers['content-type']!=='application/json')return send(400,{error:'Use a supported action request.'})
    let body;try{let bytes=0,chunks=[];for await(const chunk of req){bytes+=chunk.length;if(bytes>16384)throw new Error('Too large');chunks.push(chunk)}body=JSON.parse(Buffer.concat(chunks).toString())}catch{return send(400,{error:'Invalid bounded action request.'})}
    if(url.pathname.endsWith('/preview')){
     let request;try{request=actionInput(body,claims,definition)}catch(error){return send(error.status??400,{error:error.status===403?'Support action permission required.':'Invalid action fields.'})}
     auditContext.resource=request.r.id
     const rows=await readRows(previewQuery(config,definition,request.action,request.r,request.input.keys))
     if(rows.length!==1)return send(409,{error:'Case is unavailable or keys are not unique.'})
     const row=rows[0];let caseId,version;try{caseId=decodeJsonCell(exactJSON(row[request.action.idColumn]));version=decodeJsonCell(exactJSON(row[request.action.version]))}catch{return send(422,{error:'Actions require safe integer case IDs and versions.'})}
     if(!Number.isInteger(caseId)||caseId<1||caseId>2147483647||!Number.isInteger(version)||version<1||version>=2147483647)return send(422,{error:'Actions require valid integer case IDs and versions.'})
     const id=randomUUID(),reviewExpires=Date.now()+300000
     const requestData={...request.input,caseId,version,reviewExpires,id}
     const receipt=seal({kind:'action',sub:claims.sub,tenant:claims.tenant,binding:sessionBinding(session.token),expires:Math.min(session.expires,Date.now()+1800000),id,request:requestData},config.key)
     if(receipt.length>3500)return send(422,{error:'Reply is too large for a confirmation. Use a shorter reply.'})
     await actions.register(claims,requestData)
     return send(200,{receipt,id,preview:{label:request.action.label,resource:request.r.label,case_id:caseId,version,body:request.input.body??null},expires_in:300})
    }
    if(!body||Object.keys(body).length!==1||typeof body.receipt!=='string')return send(400,{error:'Confirm the reviewed action receipt.'})
    const receipt=unseal(body.receipt,config.key,'action')
    if(!receipt||receipt.sub!==claims.sub||receipt.tenant!==claims.tenant||receipt.binding!==sessionBinding(session.token))return send(409,{error:'Confirmation expired or belongs to another session. Preview again.'})
    let request;try{request=actionInput({action:receipt.request.action,keys:receipt.request.keys,...(receipt.request.body!==undefined?{body:receipt.request.body}:{})},claims,definition)}catch{return send(403,{error:'Support action permission required.'})}
    auditContext.resource=request.r.id;auditContext.action='support.'+request.action.id
    const attempt=auditEvent({claims,action:'support.attempt',status:200,resource:request.r.id})
    if(durable&&!await durable.record(attempt))return send(503,{error:'Audit delivery cannot be queued. No action was submitted.'})
    audit.accept(attempt)
    const result=await actions.execute(claims,{...receipt.request,id:receipt.id})
    return send(200,{result:actionResult(result,request.action.id)})
   }
   if(url.pathname==='/api/customer'){
    let request;try{request=customerRequest(config,definition,url.searchParams)}catch{return send(400,{error:'Choose an approved customer lookup or profile.'})}
    auditContext.resource=request.r.id
    const rows=await readRows(request.query)
    if(!request.profile)return send(200,exactJSON(rows))
    if(!rows.length)return send(404,{error:'Customer is unavailable under your permissions.'})
    if(rows.length!==1)return send(409,{error:'Customer keys must uniquely identify one record.'})
    const row=rows[0],related=[]
    for(const id of request.c.relationships){
     const rel=request.r.relationships.find(rel=>rel.id===id)
     let values;try{values=rel.from.map(column=>decodeJsonCell(exactJSON(row[column])))}catch{return send(422,{error:'Customer relationships require safe scalar keys.'})}
     let query;try{query=dataTarget(config,definition,new URLSearchParams({resource:rel.target,limit:'6',scope:JSON.stringify({source:request.r.id,relationship:id,values})}))}catch{return send(422,{error:'Invalid customer relationship values.'})}
     const records=await readRows(query);related.push({relationship:id,resource:rel.target,rows:records.slice(0,5),more:records.length>5})
    }
    const body=exactJSON({row,related});if(Buffer.byteLength(body)>1048576)return send(422,{error:'Customer profile exceeds response bounds. Narrow approved columns or relationships.'});return send(200,body)
   }
   if(url.pathname==='/api/timeline'){
    let request;try{request=timelineRequest(definition,url.searchParams)}catch{return send(400,{error:'Choose an approved timeline and customer mapping.'})}
    auditContext.resource=request.t.resource
    const owner=createHash('sha256').update(session.token).digest('hex'),scope=JSON.stringify([request.t.id,request.values])
    for(const [key,value] of snapshots)if(value.expires<Date.now())snapshots.delete(key)
    let snapshot=request.snapshot?snapshots.get(request.snapshot):null
    if(request.snapshot&&(!snapshot||snapshot.owner!==owner||snapshot.scope!==scope))return send(409,{error:'Timeline expired or belongs to another session. Open it again.'})
    if(!request.snapshot&&request.offset)return send(400,{error:'Start from the first timeline page.'})
    let events=[]
    for(const source of request.t.sources){if(definition.resources.find(r=>r.id===source.resource)?.staffOnly&&claims.gregale_internal_notes!==true)continue;const query=sourceQuery(config,definition,source,request.values);events.push(...eventsFor(source,query.resource,await readRows(query)))}
    events=orderEvents(events)
    if(Buffer.byteLength(exactJSON(events))>1048576)throw new Error('Timeline exceeds response bounds.')
    if(!snapshot){
     if(snapshots.size>=20)snapshots.delete(snapshots.keys().next().value)
     const id=randomBytes(24).toString('base64url');snapshot={id,owner,scope,expires:Date.now()+300000,ids:events.map(e=>e.id)};snapshots.set(id,snapshot)
    }
    const current=new Map(events.map(event=>[event.id,event])),ids=snapshot.ids.slice(request.offset,request.offset+25)
    return send(200,'{"snapshot":'+JSON.stringify(snapshot.id)+',"events":'+exactJSON(ids.map(id=>current.get(id)).filter(Boolean))+',"next":'+JSON.stringify(request.offset+25<snapshot.ids.length?request.offset+25:null)+',"total":'+snapshot.ids.length+'}')
   }
   let query;try{const params=new URLSearchParams(url.searchParams);if(params.has('purpose')){if(params.getAll('purpose').length!==1||params.get('purpose')!=='export')throw new Error('Invalid read purpose.');params.delete('purpose')}const queue=params.get('queue');if(params.has('queue')){if(params.getAll('queue').length!==1||!['mine','unassigned','due_soon','overdue'].includes(queue)||params.get('resource')!==definition.queues?.resource)throw new Error('Invalid queue.');params.delete('queue')}query=dataTarget(config,definition,params);if(queue){if(['mine','unassigned'].includes(queue))query.target.searchParams.append(definition.queues.assignee,queue==='mine'?'eq.'+claims.sub:'is.null');else{if(!definition.queues.deadline)throw new Error('Deadline queues are not enabled.');const now=Date.now();query.target.searchParams.append(definition.queues.firstResponse,'is.null');query.target.searchParams.append(definition.queues.deadline,(queue==='overdue'?'lt.':'gte.')+new Date(now).toISOString());if(queue==='due_soon')query.target.searchParams.append(definition.queues.deadline,'lte.'+new Date(now+definition.queues.dueSoonMinutes*60000).toISOString())}query.target.searchParams.append(definition.queues.status,'eq.open')}auditContext.resource=query.resource.id}catch{return send(400,{error:'Choose an approved resource and supported query.'})}
   return send(200,exactJSON(await readRows(query)))
  })().catch(error=>{if(!res.headersSent){if(req.url.startsWith('/auth/callback'))signInError(502);else if(error.status)send(error.status,{error:error.message});else send(502,{error:'Support service unavailable or sign-in refused. Try again or contact your administrator.'})}else res.end()})
 })
 if(durable)server.on('close',()=>void durable.close())
 if(config.actionDatabase)server.on('close',()=>void actions.close())
 return server
}
if(process.argv[1]&&pathToFileURL(process.argv[1]).href===import.meta.url){try{const config=settings(process.env),definition=JSON.parse(await readFile(new URL('./support-console.json',import.meta.url))),server=await createConsole(config,definition);server.listen(config.port,'0.0.0.0',()=>console.log('Support console listening.'))}catch{console.error('Support console configuration failed. Review the configured identity provider, endpoints, session key and approved views.');process.exitCode=1}}
