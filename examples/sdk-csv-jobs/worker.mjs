import {readFile} from 'node:fs/promises';
import {pathToFileURL} from 'node:url';
import {resolve} from 'node:path';
import {csvHandler} from './handler.mjs';

const runtime=process.env.GREGALE_NODE_JOB_RUNTIME?pathToFileURL(resolve(process.env.GREGALE_NODE_JOB_RUNTIME)):new URL('../../sdk/node/dist/job-runtime.js',import.meta.url);
const {JobContract,runJobTask,localJobStore}=await import(runtime.href);
const contract=new JobContract(JSON.parse(await readFile(new URL('contract.json',import.meta.url),'utf8')));
// Local integration uses private files. In a deployed image, provide a module
// exporting a JobStore backed by your object store, with its own credentials.
const store=process.env.GREGALE_LOCAL_JOB_STORE?localJobStore(process.env.GREGALE_LOCAL_JOB_STORE):(await import(pathToFileURL(resolve(process.env.GREGALE_JOB_STORE_MODULE)).href)).store;
const handler=csvHandler(process.env.DATABASE_URL);
try{await runJobTask(contract,handler.handle,{timeoutMs:60000,store});}
catch(error){console.error(JSON.stringify({code:error.code??'handler_failed'}));process.exitCode=1;}
finally{await handler.close();}
