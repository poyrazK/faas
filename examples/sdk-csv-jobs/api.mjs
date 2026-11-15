import http from 'node:http';
import {readFile,mkdir,writeFile,rename,unlink} from 'node:fs/promises';
import {resolve,join} from 'node:path';
import {createHash,randomUUID} from 'node:crypto';
import {pathToFileURL} from 'node:url';
import {csvInputs} from './csv.mjs';

const sdk=await import(process.env.GREGALE_NODE_SDK?pathToFileURL(resolve(process.env.GREGALE_NODE_SDK)).href:new URL('../../sdk/node/dist/index.js',import.meta.url).href);
const contract=new sdk.JobContract(JSON.parse(await readFile(new URL('contract.json',import.meta.url),'utf8')));
const client=sdk.createClient(process.env.FAAS_API,{token:process.env.FAAS_TOKEN});
const job=client.jobs.defineJob(contract,process.env.GREGALE_LOCAL_JOB_STORE?{store:sdk.localJobStore(process.env.GREGALE_LOCAL_JOB_STORE)}:{});
const directory=resolve(process.env.GREGALE_IMPORT_STATE??'.imports'),token=process.env.GREGALE_IMPORT_APP_TOKEN;
if(!token)throw new Error('Set GREGALE_IMPORT_APP_TOKEN independently of FAAS_TOKEN');
await mkdir(directory,{recursive:true,mode:0o700});
const options={timeoutMs:60000,maxItems:1000},busy=new Set();
async function json(path){try{return JSON.parse(await readFile(path,'utf8'));}catch(error){if(error.code==='ENOENT')return null;throw error;}}
const server=http.createServer(async(request,response)=>{
  const send=(status,value)=>{response.writeHead(status,{'Content-Type':'application/json'});response.end(JSON.stringify(value));};
  if(request.headers.authorization!=='Bearer '+token){send(401,{code:'unauthorized'});return;}
  const route=/^\/imports\/([A-Za-z0-9_.:-]{1,80})(?:\/(rejections.csv))?$/.exec(new URL(request.url,'http://localhost').pathname);
  if(!route){send(404,{code:'not_found'});return;}
  const id=route[1],path=join(directory,id+'.json');
  try{
    if(request.method==='POST'&&!route[2]){
      if(busy.has(id)){send(409,{code:'import_busy'});return;}
      busy.add(id);
      try{
        let size=0;const chunks=[];for await(const chunk of request){size+=chunk.length;if(size>1024*1024){send(413,{code:'csv_limit'});return;}chunks.push(chunk);}
        const data=Buffer.concat(chunks),digest=createHash('sha256').update(data).digest('hex');
        const marker=join(directory,id+'.prepared'),prepared=await json(marker),existing=await json(path);
        if(prepared&&prepared.sha256!==digest){send(409,{code:'import_changed'});return;}
        if(existing){send(200,{runId:existing.run_id});return;}
        if(prepared){send(409,{code:'submission_outcome_unknown'});return;}
        const inputs=csvInputs(new TextDecoder('utf-8',{fatal:true}).decode(data),id,process.env.GREGALE_CSV_FAIL_ROW);
        for(const input of inputs)contract.input(input.value);
        if(new Set(inputs.map(i=>i.id)).size!==inputs.length)throw new Error('Duplicate CSV row');
        // Persist intent first. An uncertain outcome is inspected explicitly.
        await writeFile(marker,JSON.stringify({sha256:digest,idempotency_key:id}),{mode:0o600,flag:'wx'});
        const run=await job.submit(inputs,{...options,idempotencyKey:id,parallelism:2,retryMax:0});
        const temporary=join(directory,'.'+randomUUID());
        try{await writeFile(temporary,JSON.stringify(run.checkpoint),{mode:0o600,flag:'wx'});await rename(temporary,path);}finally{await unlink(temporary).catch(()=>{});}
        send(202,{runId:run.checkpoint.run_id});
      }finally{busy.delete(id);}
    }else if(request.method==='GET'){
      const checkpoint=await json(path);if(!checkpoint){send(404,{code:'not_found'});return;}
      const run=job.resume(sdk.parseJobCheckpoint(checkpoint));
      if(!route[2])send(200,await run.status(options));
      else{
        const results=await run.results(options);if(results.some(r=>r.outcome==='invalid_result')){send(409,{code:'result_unavailable'});return;}
        const rows=results.filter(r=>r.result?.rejection==='invalid_email').map(r=>r.inputId+',invalid_email');
        response.writeHead(200,{'Content-Type':'text/csv','Content-Disposition':'attachment; filename="rejections.csv"'});response.end('input_id,reason\n'+rows.join('\n')+'\n');
      }
    }else send(405,{code:'method_not_allowed'});
  }catch(error){send(400,{code:error.code??'invalid_import'});}
});
server.listen(Number(process.env.PORT??8080),'0.0.0.0');
process.on('SIGTERM',()=>server.close());
