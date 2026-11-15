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
  const form=document.querySelector('form'),status=document.querySelector('#status'),start=document.querySelector('#start'),resume=document.querySelector('#resume'),pause=document.querySelector('#pause'),discard=document.querySelector('#discard'),report=document.querySelector('#report');
  let controller,record,checkpoint,running=false;
  const request=async(path,init={})=>{const response=await fetch(path,{...init,credentials:'same-origin',headers:{'Content-Type':'application/json',Authorization:'Bearer '+document.querySelector('#token').value}});if(!response.ok){const body=await response.json();throw new Error(body.code);}return response.json();};
  function save(){localStorage.setItem('gregale-file-import',JSON.stringify({record,checkpoint}));}
  try{({record,checkpoint}=JSON.parse(localStorage.getItem('gregale-file-import')??'{}'));if(checkpoint)checkpoint=parseFileCheckpoint(checkpoint);}catch{record=checkpoint=undefined;}
  const run=async continuing=>{
    const file=document.querySelector('#file').files[0];if(!file)throw new Error('Choose the same CSV file to upload or resume');
    controller=new AbortController();report.disabled=true;const source=blobFileSource(file);
    if(!continuing){const source_digest=await fileSourceDigest(source,controller.signal);record=await request('/files',{method:'POST',body:JSON.stringify({size_bytes:file.size,source_digest})});checkpoint=undefined;save();}
    if(!record)throw new Error('No saved import');
    const files=createFileBucket(record.scope,fileBroker(record.id,request));
    const options={timeoutMs:120000,signal:controller.signal,maxBytes:1024*1024,onCheckpoint:c=>{checkpoint=c;save();},onProgress:p=>{status.textContent=`Uploaded ${p.confirmed_bytes} / ${p.total_bytes} bytes`;}};
    if(checkpoint)await files.resume(checkpoint,source,options);else await files.upload(record.key,source,{...options,contentType:'text/csv'});
    for(let i=0;i<120;i++){
      controller.signal.throwIfAborted();const p=await request(`/files/${record.id}/progress`,{signal:controller.signal});status.textContent=`${p.status}: ${p.succeeded}/${p.total} rows saved or validated; ${p.failed} failed`;
      if(p.status==='submission_unresolved')throw new Error('Submission needs explicit recovery by run ID. The upload is saved.');
      if(['succeeded','failed','cancelled','dead_letter'].includes(p.status)){report.disabled=false;return;}
      await new Promise(r=>setTimeout(r,500));
    }throw new Error('Polling limit reached; resume to inspect progress');
  };
  const show=work=>{if(running)return;running=true;start.disabled=resume.disabled=discard.disabled=true;work().catch(e=>{status.textContent=e.message;}).finally(()=>{running=false;start.disabled=resume.disabled=discard.disabled=false;});};
  form.addEventListener('submit',e=>{e.preventDefault();show(()=>run(false));});
  resume.addEventListener('click',()=>show(()=>run(true)));pause.addEventListener('click',()=>controller?.abort());
  discard.addEventListener('click',()=>show(async()=>{controller?.abort();if(checkpoint)await createFileBucket(record.scope,fileBroker(record.id,request)).abort(checkpoint,{timeoutMs:30000});localStorage.removeItem('gregale-file-import');record=checkpoint=undefined;status.textContent='Upload discarded';}));
  report.addEventListener('click',()=>show(async()=>{const r=await fetch(`/files/${record.id}/rejections`,{headers:{Authorization:'Bearer '+document.querySelector('#token').value}});if(!r.ok)throw new Error((await r.json()).code);const url=URL.createObjectURL(await r.blob()),a=document.createElement('a');a.href=url;a.download='rejections.csv';a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);}));
  if(record)status.textContent='Saved import found. Enter your application token, choose the same CSV, then resume.';
}
