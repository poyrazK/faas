// Application authorization stays in file-api.mjs. This bridge never accepts a
// channel, file key, job ID or platform credential from a browser.
import {createHash} from 'node:crypto';
const terminal=new Set(['succeeded','failed','cancelled','dead_letter']);
const safeCode=value=>typeof value==='string'&&/^[A-Za-z0-9_.:-]{1,256}$/.test(value)?value:'task_failed';

export async function rejectionReport({record,run,files,signal}){
  const results=await run.results({timeoutMs:10000,signal,maxItems:1000});
  if(results.some(r=>r.outcome==='invalid_result'||r.outcome==='pending'))return {state:'unavailable',failures:[]};
  const failures=results.filter(r=>r.outcome==='failed'||r.outcome==='cancelled').map(r=>({input_id:r.inputId,code:safeCode(r.code)}));
  const rejected=results.filter(r=>r.result?.rejection==='invalid_email').map(r=>r.inputId+',invalid_email');
  const bytes=new TextEncoder().encode('input_id,reason\n'+rejected.join('\n')+'\n'),key='imports/'+record.id+'/rejections.csv';
  // Report-ready requires both native terminal status and a verified object.
  try{await files.put(key,bytes,{timeoutMs:10000,signal,contentType:'text/csv',ifNoneMatch:'*'});}catch(e){if(![409,412].includes(e.status))throw e;}
  const actual=await files.download(key,{timeoutMs:10000,signal,maxBytes:1024*1024});
  if(Buffer.compare(Buffer.from(actual),Buffer.from(bytes))!==0)throw new Error('report_changed');
  return {state:'ready',bytes:actual,failures};
}

export function createFileLive({client,files,job,sdk,load,refresh,users,config}){
  const channel=id=>'import:'+id;
  const bridge=sdk.createLiveJobBridge({pollDelayMs:config.pollDelayMs??500,leaseMs:35000,requestTimeoutMs:15000,maxResources:128,
    loadSnapshot:async(id,signal)=>{
      const record=await load(id);if(!record||!Object.hasOwn(users,record.owner))throw new Error('unowned_import');
      await refresh(record);
      const empty={version:1,resource_id:id,run_id:null,total:0,succeeded:0,failed:0,cancelled:0,running:0,report:'none',failures:[]};
      if(!record.run)return {...empty,status:record.pipeline_unresolved?'submission_unresolved':'awaiting_storage_event'};
      const run=job.resume(sdk.parseJobCheckpoint(record.run)),p=await run.status({timeoutMs:10000,signal});
      let report={state:terminal.has(p.status)?'pending':'none',failures:[]};
      if(terminal.has(p.status))report=await rejectionReport({record,run,files,signal});
      return {...empty,run_id:record.run.run_id,status:p.status,total:p.total,succeeded:p.succeeded,failed:p.failed,cancelled:p.cancelled,running:p.running,report:report.state,failures:report.failures};
    },
    publish:async(snapshot,event,signal)=>{
      const key='import-live:'+createHash('sha256').update(JSON.stringify(snapshot)).digest('hex');
      await client.realtime.publishManagedRealtimeChannel({slug:files.scope.app_slug,id:config.endpointId,channel:channel(snapshot.resource_id),delivery:'retained',idempotencyKey:key,requestBody:{data_base64:Buffer.from(JSON.stringify(event)).toString('base64'),binary:false}},{timeoutMs:10000,signal,retry:false});
    },
  });
  return {
    snapshot:id=>bridge.snapshot(id),
    async ticket(record){
      const value=await client.realtime.issueRealtimeSubscriptionTicket({slug:files.scope.app_slug,id:config.endpointId,requestBody:{principal:record.owner,channels:[channel(record.id)],ttl_seconds:30}},{timeoutMs:10000,retry:false});
      bridge.watch(record.id);
      return {url:config.url,channel:channel(record.id),...value};
    },
    close:()=>bridge.close(),
  };
}
