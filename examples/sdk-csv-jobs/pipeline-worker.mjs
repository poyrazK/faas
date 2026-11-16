import {readFile} from 'node:fs/promises';
import {pathToFileURL} from 'node:url';
import {resolve} from 'node:path';
import {csvInputs} from './csv.mjs';
import {csvHandler} from './handler.mjs';
const runtime=process.env.GREGALE_NODE_JOB_RUNTIME?pathToFileURL(resolve(process.env.GREGALE_NODE_JOB_RUNTIME)):new URL('../../sdk/node/dist/job-runtime.js',import.meta.url);
const sdk=await import(new URL('./preview.js',runtime)),{jobHash}=await import(runtime);
const stage=process.env.GREGALE_CSV_PIPELINE_STAGE;
if(!['validate','import','report'].includes(stage))throw new Error('Invalid pipeline stage');
const filename=stage==='import'?'contract.json':stage+'-contract.json';
const contract=new sdk.JobContract(JSON.parse(await readFile(new URL(filename,import.meta.url),'utf8')));
const client=sdk.createClient(process.env.FAAS_API,{token:process.env.FAAS_TOKEN});
const files=client.storage.files({appSlug:'imports',bucket:process.env.GREGALE_FILE_BUCKET}),store=await files.jobStore({timeoutMs:10000});
const importer=stage==='import'?csvHandler(process.env.DATABASE_URL):null;
async function handle(input,context){
  context.signal.throwIfAborted();
  if(stage==='import')return importer.handle(input,context);
  if(!/^[a-f0-9-]{36}$/.test(input.import_id))throw new Error('Invalid import identity');
  if(stage==='validate'){
    const bytes=await sdk.readPipelineArtifact(files,input.source,input.source.owner_id,{timeoutMs:10000,signal:context.signal,maxBytes:1024*1024});
    return {rows:csvInputs(new TextDecoder('utf-8',{fatal:true}).decode(bytes),input.import_id)};
  }
  const key='pipelines/'+input.import_id+'/rejections.csv',bytes=new TextEncoder().encode('input_id,reason\n'+input.rejections.map(id=>id+',invalid_email').join('\n')+'\n');
  let actual;
  try{actual=await files.download(key,{timeoutMs:10000,signal:context.signal,maxBytes:1024*1024});}
  catch(error){if(error.status!==404)throw error;await files.put(key,bytes,{timeoutMs:10000,signal:context.signal,ifNoneMatch:'*',contentType:'text/csv'});actual=await files.download(key,{timeoutMs:10000,signal:context.signal,maxBytes:1024*1024});}
  if(actual.length!==bytes.length||jobHash(actual)!==jobHash(bytes))throw new Error('Report changed');
  return {version:1,scope:files.scope,owner_id:input.owner_id,key,size_bytes:bytes.length,sha256:jobHash(bytes)};
}
try{await sdk.runJobTask(contract,handle,{timeoutMs:60000,store});}
catch(error){console.error(JSON.stringify({code:error.code??'handler_failed'}));process.exitCode=1;}
finally{await importer?.close();}
