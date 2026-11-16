import {readFile} from 'node:fs/promises';
import {pathToFileURL} from 'node:url';
import {resolve} from 'node:path';
import {csvHandler} from './handler.mjs';

const runtime=process.env.GREGALE_NODE_JOB_RUNTIME?pathToFileURL(resolve(process.env.GREGALE_NODE_JOB_RUNTIME)):new URL('../../sdk/node/dist/job-runtime.js',import.meta.url);
const {JobContract,runJobTask,localJobStore}=await import(runtime.href);
const contract=new JobContract(JSON.parse(await readFile(new URL('contract.json',import.meta.url),'utf8')));
// Managed files remove the need for an application-authored storage adapter.
let store;
if(process.env.GREGALE_FILE_BUCKET){
  const {createClient}=await import(new URL('./preview.js',runtime));
  store=await createClient(process.env.FAAS_API,{token:process.env.GREGALE_FILE_TOKEN??process.env.FAAS_TOKEN}).storage.files({appSlug:process.env.GREGALE_FILE_APP,bucket:process.env.GREGALE_FILE_BUCKET}).jobStore({timeoutMs:60000});
}else store=process.env.GREGALE_LOCAL_JOB_STORE?localJobStore(process.env.GREGALE_LOCAL_JOB_STORE):(await import(pathToFileURL(resolve(process.env.GREGALE_JOB_STORE_MODULE)).href)).store;
const handler=csvHandler(process.env.DATABASE_URL);
try{await runJobTask(contract,handler.handle,{timeoutMs:60000,store});}
catch(error){console.error(JSON.stringify({code:error.code??'handler_failed'}));process.exitCode=1;}
finally{await handler.close();}
