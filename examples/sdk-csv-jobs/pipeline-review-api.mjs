/** Authenticated application routes; platform credentials stay on this server. */
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {createHash} from 'node:crypto';

export function createPipelineReviewServer({sdk,client,scope,files,contract,pipeline,reviewContract,gateStore,users,realtime,nativeEntry,token}){
  if(typeof token!=='string'||!token)throw new Error('backend_token_required');
  const leases=new Map(),options={timeoutMs:10000};
  const principal=(name,token)=>{const u=users[name];if(!u||u.token!==token)throw new Error('unauthorized');return u;};
  const typed=ownerId=>sdk.createJobPipelineClient({baseURL:scope.base_url,token,scope,ownerId,contract});
  const gates=sdk.createPipelineGateService({scope,pipelineSha256:contract.sha256,contract:reviewContract,store:gateStore,
    transport:sdk.nativePipelineGateTransport({scope,token,contract,timeoutMs:10000}),
    authorize:async(_record,_action,access)=>{const u=principal(access.name,access.token);return {actor_id:access.name,owner_id:u.owner_id,roles:u.roles};},
  });
  const channel=id=>'pipeline:'+id;
  const matching=async(id,owner,signal)=>{
    const rows=await gateStore.list(id,owner,signal);
    const row=rows.find(r=>r.intent.name==='review');
    if(row&&(row.intent.pipeline_sha256!==contract.sha256||row.intent.contract_sha256!==reviewContract.sha256||['base_url','account_id','app_id','app_slug'].some(k=>row.intent.scope[k]!==scope[k])))throw new Error('scope_changed');
    return row;
  };
  const bridge=sdk.createLiveJobBridge({pollDelayMs:realtime?.pollDelayMs??500,leaseMs:35000,requestTimeoutMs:15000,maxResources:128,
    loadSnapshot:async(id,signal)=>{
      const access=leases.get(id);if(!access)throw new Error('unauthorized');const u=principal(access.name,access.token);
      const run=await typed(u.owner_id).resumeById(id,{...options,signal});
      let snapshot=await sdk.jobPipelineLiveSnapshot(run,{resourceId:id,steps:['validate','import','report'],...options,signal,inspectJob:(step,active)=>pipeline.inspectJob(id,step,u.owner_id,{...options,signal:active})});
      const gate=await matching(id,u.owner_id,signal);
      if(gate&&!gate.decision&&['running','queued'].includes(snapshot.status))snapshot={...snapshot,waiting:{kind:'approval',gate_id:gate.intent.id,name:'review'}};
      if(snapshot.status==='succeeded'){
        const output=await run.result({...options,signal});
        if(output.status==='completed'){await sdk.readPipelineArtifact(files,output.report,u.owner_id,{...options,signal,maxBytes:1024*1024});snapshot={...snapshot,report:'ready'};}
      }
      principal(access.name,access.token);return snapshot;
    },
    publish:async(snapshot,event,signal)=>{
      if(!realtime)return;
      await client.realtime.publishManagedRealtimeChannel({slug:scope.app_slug,id:realtime.endpointId,channel:channel(snapshot.resource_id),delivery:'retained',idempotencyKey:'pipeline-live:'+createHash('sha256').update(JSON.stringify(snapshot)).digest('hex'),requestBody:{data_base64:Buffer.from(JSON.stringify(event)).toString('base64'),binary:false}},{...options,signal,retry:false});
    },
  });
  // Durable delivery is independent of an open browser or workflow process.
  let draining=false;
  const timer=setInterval(async()=>{if(draining)return;draining=true;try{await gates.drain(100,{timeoutMs:10000});}catch{}finally{draining=false;}},1000);timer.unref();
  const server=http.createServer(async(req,res)=>{
    const path=new URL(req.url,'http://localhost').pathname;
    if(nativeEntry&&req.method==='POST'&&path==='/csv-reviewed-pipeline')return nativeEntry(req,res);
    const send=(status,value)=>{if(res.destroyed)return;res.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'});res.end(JSON.stringify(value));};
    if(req.method==='GET'&&(path==='/'||path==='/pipeline-review-browser.mjs'||/^\/sdk\/(live-jobs|live-job-realtime|browser-realtime|realtime-resume|realtime-expiry|realtime-activity)\.js$/.test(path))){
      try{const file=path==='/'?new URL('pipeline-review.html',import.meta.url):path.startsWith('/sdk/')?new URL('../../sdk/node/dist/'+path.slice(5),import.meta.url):new URL('pipeline-review-browser.mjs',import.meta.url);res.writeHead(200,{'Content-Type':path==='/'?'text/html':'text/javascript','Cache-Control':'no-store','X-Content-Type-Options':'nosniff'});res.end(await readFile(file));}catch{send(404,{code:'not_found'});}return;
    }
    const token=req.headers.authorization?.replace(/^Bearer /,''),name=Object.keys(users).find(n=>users[n].token===token);
    if(!name){send(401,{code:'unauthorized'});return;}
    const route=/^\/runs\/([a-f0-9-]{36})\/(snapshot|ticket|gate|decision|report)$/.exec(path);
    if(!route){send(404,{code:'not_found'});return;}
    const abort=new AbortController();req.once('aborted',()=>abort.abort());res.once('close',()=>{if(!res.writableEnded)abort.abort();});const signal=abort.signal;
    try{
      const access={name,token},u=principal(name,token),id=route[1],action=route[2];
      // Verify native root ownership before revealing even a pending gate ID.
      const run=await typed(u.owner_id).resumeById(id,{...options,signal});principal(name,token);
      if(req.method==='GET'&&action==='snapshot'){leases.set(id,access);send(200,await bridge.snapshot(id));return;}
      if(req.method==='GET'&&action==='ticket'){
        if(!realtime){send(503,{code:'realtime_not_configured'});return;}
        const value=await client.realtime.issueRealtimeSubscriptionTicket({slug:scope.app_slug,id:realtime.endpointId,requestBody:{principal:name,channels:[channel(id)],ttl_seconds:30}},{...options,signal,retry:false});principal(name,token);leases.set(id,access);bridge.watch(id);send(200,{...value,url:realtime.url,channel:channel(id)});return;
      }
      const row=await matching(id,u.owner_id,signal);
      if(req.method==='GET'&&action==='gate'){const gate=row?await gates.inspect(row.intent.id,access,{...options,signal}):null;principal(name,token);send(200,{gate});return;}
      if(req.method==='POST'&&action==='decision'){
        let length=0;const chunks=[];for await(const b of req){length+=b.length;if(length>32768)throw new Error('invalid_request');chunks.push(b);}
        const request=JSON.parse(Buffer.concat(chunks).toString());if(!row||request.gate_id!==row.intent.id){send(404,{code:'not_found'});return;}
        const gate=await gates.decide(row.intent.id,{binding_sha256:request.binding_sha256,state:request.state,payload:request.payload},access,{...options,signal});principal(name,token);send(200,{gate});return;
      }
      if(req.method==='GET'&&action==='report'){
        const output=await run.result({...options,signal});if(output.status!=='completed')throw new Error('report_unavailable');
        const bytes=await sdk.readPipelineArtifact(files,output.report,u.owner_id,{...options,signal,maxBytes:1024*1024});principal(name,token);res.writeHead(200,{'Content-Type':'text/csv','Cache-Control':'no-store'});res.end(bytes);return;
      }
      send(404,{code:'not_found'});
    }catch(error){send(error.code==='conflict'?409:404,{code:error.code==='conflict'?'decision_conflict':'not_found'});}
  });
  server.once('close',()=>{clearInterval(timer);bridge.close();leases.clear();});
  return server;
}
