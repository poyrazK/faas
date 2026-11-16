import {readFile,writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {pathToFileURL} from 'node:url';
import {existsSync} from 'node:fs';
import {csvInputs} from './csv.mjs';
import {createHash} from 'node:crypto';
const sdk=await import(process.env.GREGALE_NODE_SDK?pathToFileURL(resolve(process.env.GREGALE_NODE_SDK)).href:new URL('../../sdk/node/dist/index.js',import.meta.url).href);
const contract=new sdk.JobContract(JSON.parse(await readFile(new URL('contract.json',import.meta.url),'utf8')));
const client=sdk.createClient(process.env.FAAS_API,{token:process.env.FAAS_TOKEN});
const job=client.jobs.defineJob(contract,process.env.GREGALE_LOCAL_JOB_STORE?{store:sdk.localJobStore(process.env.GREGALE_LOCAL_JOB_STORE)}:{});
const mode=process.argv[2]??'submit',file=resolve(process.env.GREGALE_JOB_CHECKPOINT??'job-run.json'),options={timeoutMs:120000,maxItems:10000};
try{
  let run;
  if(mode==='submit'){
    if(existsSync(file))throw new Error('Choose another checkpoint for a new submission');
    const source=await readFile(process.argv[3],'utf8');
    const inputs=process.argv[3].endsWith('.csv')?csvInputs(source,process.env.GREGALE_IMPORT_ID??('import-'+createHash('sha256').update(source).digest('hex').slice(0,16)),process.env.GREGALE_CSV_FAIL_ROW):JSON.parse(source);
    run=await job.submit(inputs,{...options,idempotencyKey:process.env.GREGALE_JOB_SUBMISSION_KEY,parallelism:2,retryMax:0});
    await writeFile(file,JSON.stringify(run.checkpoint),{mode:0o600,flag:'wx'});
    console.log(JSON.stringify({runId:run.checkpoint.run_id}));
  }else{
    run=job.resume(sdk.parseJobCheckpoint(JSON.parse(await readFile(file,'utf8'))));
    if(mode==='replay'){
      run=await run.retryFailed({...options,idempotencyKey:process.env.GREGALE_JOB_SUBMISSION_KEY});
      const next=resolve(process.env.GREGALE_JOB_REPLAY_CHECKPOINT??'job-replay.json');await writeFile(next,JSON.stringify(run.checkpoint),{mode:0o600,flag:'wx'});
      console.log(JSON.stringify({runId:run.checkpoint.run_id,checkpoint:next}));
    }else if(mode==='cancel')console.log(JSON.stringify(await run.cancel(options)));
    else if(mode==='wait')console.log(JSON.stringify(await run.wait({...options,maxPolls:100,onProgress:p=>console.error(JSON.stringify(p))})));
    else if(mode==='results')console.log(JSON.stringify(await run.results(options)));
    else throw new Error('Use submit, wait, results, replay or cancel');
  }
}catch(error){console.error(JSON.stringify({code:error.code??'request_failed'}));process.exitCode=1;}
