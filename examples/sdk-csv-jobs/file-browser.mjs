/** Same application broker is exercised by the local integration test. */
export function fileBroker(id,request){
  const post=(action,value,signal)=>request(`/files/${id}/${action}`,{method:'POST',body:JSON.stringify(value),signal});
  const unsupported=async()=>{throw new Error('This upload-only application route does not support this operation');};
  return {
    begin:(key,size,contentType,signal)=>post('begin',{key,size,contentType},signal),
    session:(upload,signal)=>post('session',{id:upload},signal),
    parts:(upload,marker,signal)=>post('parts',{id:upload,marker},signal),
    signPart:(upload,part,signal)=>post('sign-part',{id:upload,part},signal),
    complete:(upload,parts,signal)=>post('complete',{id:upload,parts},signal),
    abort:(upload,signal)=>post('abort',{id:upload},signal),
    sign:unsupported,receipt:unsupported,list:unsupported,delete:unsupported,
  };
}
if(typeof document!=='undefined'){
  const {createFileBucket,blobFileSource,fileSourceDigest,parseFileCheckpoint}=await import('/sdk-files.js');
  const {createLiveJob}=await import('/sdk/live-jobs.js');
  const {realtimeLiveJobSource}=await import('/sdk/live-job-realtime.js');
  const {createBrowserRealtimeTicketSocketFactory}=await import('/sdk/browser-realtime.js');
  const {RealtimeConfigurationError}=await import('/sdk/realtime-resume.js');
  const form=document.querySelector('form'),status=document.querySelector('#status'),start=document.querySelector('#start'),resume=document.querySelector('#resume'),pause=document.querySelector('#pause'),discard=document.querySelector('#discard'),report=document.querySelector('#report');
  let controller,live,record,checkpoint,running=false;
  const request=async(path,init={})=>{const response=await fetch(path,{...init,credentials:'same-origin',headers:{'Content-Type':'application/json',Authorization:'Bearer '+document.querySelector('#token').value}});if(!response.ok){const body=await response.json();const error=new Error(body.code);error.status=response.status;throw error;}return response.json();};
  function save(){localStorage.setItem('gregale-file-import',JSON.stringify({record,checkpoint}));}
  try{({record,checkpoint}=JSON.parse(localStorage.getItem('gregale-file-import')??'{}'));if(checkpoint)checkpoint=parseFileCheckpoint(checkpoint);}catch{record=checkpoint=undefined;}
  const run=async continuing=>{
    controller=new AbortController();report.disabled=true;live?.close();
    const file=document.querySelector('#file').files[0];
    if(!continuing||!record?.uploaded){
    if(!file)throw new Error('Choose the same CSV file to upload or resume');const source=blobFileSource(file);
    if(!continuing){const source_digest=await fileSourceDigest(source,controller.signal);record=await request('/files',{method:'POST',body:JSON.stringify({size_bytes:file.size,source_digest}),signal:controller.signal});checkpoint=undefined;save();}
    if(!record)throw new Error('No saved import');
    const files=createFileBucket(record.scope,fileBroker(record.id,request));
    const options={timeoutMs:120000,signal:controller.signal,maxBytes:1024*1024,onCheckpoint:c=>{checkpoint=c;save();},onProgress:p=>{status.textContent=`Uploaded ${p.confirmed_bytes} / ${p.total_bytes} bytes`;}};
    if(checkpoint)await files.resume(checkpoint,source,options);else await files.upload(record.key,source,{...options,contentType:'text/csv'});
    record.uploaded=true;save();
    }
    if(!record)throw new Error('No saved import');
    const ticket=async()=>{
      try{return await request(`/files/${record.id}/live-ticket`,{signal:controller.signal});}
      catch(e){if([401,403,404].includes(e.status))throw new RealtimeConfigurationError('Subscription authorization changed');throw e;}
    };
    const first=await ticket();controller.signal.throwIfAborted();let initial=first;
    live=createLiveJob({resourceId:record.id,timeoutMs:120000,signal:controller.signal,
      loadSnapshot:signal=>request(`/files/${record.id}/snapshot`,{signal}),
      source:realtimeLiveJobSource({url:first.url,channel:first.channel,webSocketFactory:createBrowserRealtimeTicketSocketFactory(async()=>{
        const value=initial??await ticket();initial=undefined;
        if(value.channel!==first.channel||value.url!==first.url)throw new RealtimeConfigurationError('Subscription scope changed');return value.ticket;
      })}),
    });
    for await(const p of live.updates()){
      status.textContent=`${p.status}: ${p.succeeded}/${p.total} rows saved or validated; ${p.failed} failed`;
      if(p.status==='submission_unresolved')status.textContent='Submission receipt pending. The upload is saved; explicit recovery by run ID is needed if it remains unresolved.';
      report.disabled=p.report!=='ready';
    }
  };
  const show=work=>{if(running)return;running=true;start.disabled=resume.disabled=discard.disabled=true;work().catch(e=>{status.textContent=e.message;}).finally(()=>{running=false;start.disabled=resume.disabled=discard.disabled=false;});};
  form.addEventListener('submit',e=>{e.preventDefault();show(()=>run(false));});
  resume.addEventListener('click',()=>show(()=>run(true)));pause.addEventListener('click',()=>controller?.abort());
  document.querySelector('#token').addEventListener('input',()=>{controller?.abort();live?.close();report.disabled=true;status.textContent='Access changed. Resume to authorize this import again.';});
  discard.addEventListener('click',()=>show(async()=>{controller?.abort();if(checkpoint)await createFileBucket(record.scope,fileBroker(record.id,request)).abort(checkpoint,{timeoutMs:30000});localStorage.removeItem('gregale-file-import');record=checkpoint=undefined;status.textContent='Upload discarded';}));
  report.addEventListener('click',()=>show(async()=>{const r=await fetch(`/files/${record.id}/rejections`,{headers:{Authorization:'Bearer '+document.querySelector('#token').value}});if(!r.ok)throw new Error((await r.json()).code);const url=URL.createObjectURL(await r.blob()),a=document.createElement('a');a.href=url;a.download='rejections.csv';a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);}));
  if(record)status.textContent=record.uploaded?'Saved import found. Enter your application token, then resume live progress.':'Saved upload found. Enter your application token, choose the same CSV, then resume.';
}
