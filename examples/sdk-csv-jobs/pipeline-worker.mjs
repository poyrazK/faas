import {readFile} from 'node:fs/promises';
import {pathToFileURL} from 'node:url';
import {resolve} from 'node:path';
import {csvInputs} from './csv.mjs';
import {csvHandler} from './handler.mjs';
import pg from 'pg';
import {mappingReportSpec,mappingSourceKey} from './clarification.mjs';
const runtime=process.env.GREGALE_NODE_JOB_RUNTIME?pathToFileURL(resolve(process.env.GREGALE_NODE_JOB_RUNTIME)):new URL('../../sdk/node/dist/job-runtime.js',import.meta.url);
const sdk=await import(new URL('./preview.js',runtime)),{jobHash}=await import(runtime);
const stage=process.env.GREGALE_CSV_PIPELINE_STAGE;
if(!['validate','import','report','clarification_report','clarification_report_v2'].includes(stage))throw new Error('Invalid pipeline stage');
const mapping=stage.startsWith('clarification_report'), pinned=stage==='clarification_report_v2';
const filename=stage==='import'?'contract.json':mapping?'clarification-contracts.json':stage+'-contract.json';
const spec=JSON.parse(await readFile(new URL(filename,import.meta.url),'utf8'));
const contract=new sdk.JobContract(mapping?mappingReportSpec(spec,pinned):spec);
const client=sdk.createClient(process.env.FAAS_API,{token:process.env.FAAS_TOKEN});
const files=client.storage.files({appSlug:'imports',bucket:process.env.GREGALE_FILE_BUCKET}),store=await files.jobStore({timeoutMs:10000});
const importer=stage==='import'?csvHandler(process.env.DATABASE_URL):null;
const sourceDB=pinned?new pg.Pool({connectionString:process.env.DATABASE_URL,connectionTimeoutMillis:3000,statement_timeout:5000}):null;
async function pinnedEmail(input,signal){
  const bytes=await sdk.readPinnedPipelineSource(files,input.pinned_source,input.owner_id,{
    timeoutMs:10000,signal,maxBytes:65536,
    async authorize(source){
      if(source.key!==mappingSourceKey(input.import_id))return false;
      const result=await sourceDB.query(
        'SELECT 1 FROM sdk_csv_mapping_sources WHERE import_id=$1 AND owner_id=$2 AND enabled',
        [input.import_id,input.owner_id],
      );
      return result.rowCount===1;
    },
  });
  const [header,record]=Buffer.from(bytes).toString('utf8').trim().split('\n',2);
  const column=header.split(',').indexOf(input.email_column), email=record?.split(',')[column];
  if(!email)throw new sdk.PipelineError('source_unavailable');
  return email;
}
async function handle(input,context){
  context.signal.throwIfAborted();
  if(stage==='import')return importer.handle(input,context);
  if(!/^[a-f0-9-]{36}$/.test(input.import_id))throw new Error('Invalid import identity');
  if(stage==='validate'){
    const bytes=await sdk.readPipelineArtifact(files,input.source,input.source.owner_id,{timeoutMs:10000,signal:context.signal,maxBytes:1024*1024});
    return {rows:csvInputs(new TextDecoder('utf-8',{fatal:true}).decode(bytes),input.import_id)};
  }
  if(!mapping&&process.env.GREGALE_CSV_REPORT_FAIL==='1')throw Error('Injected report failure after import');
  if(mapping&&!/^[a-f0-9-]{36}$/.test(input.run_id))throw new Error('Invalid mapping run');
  let email;
  if(pinned){
    try{email=await pinnedEmail(input,context.signal);}
    catch(error){
      if(sdk.isPinnedPipelineSourceFailure(error.code))return {state:error.code};
      throw error;
    }
  }
  const key='pipelines/'+input.import_id+'/'+(mapping?input.run_id+'-mapping.csv':'rejections.csv');
  const bytes=new TextEncoder().encode(mapping
    ? (pinned ? 'import_id,email_column,email\n'+input.import_id+','+input.email_column+','+email+'\n'
      : 'import_id,email_column\n'+input.import_id+','+input.email_column+'\n')
    : 'input_id,reason\n'+input.rejections.map(id=>id+',invalid_email').join('\n')+'\n');
  let actual;
  try{actual=await files.download(key,{timeoutMs:10000,signal:context.signal,maxBytes:1024*1024});}
  catch(error){if(error.status!==404)throw error;await files.put(key,bytes,{timeoutMs:10000,signal:context.signal,ifNoneMatch:'*',contentType:'text/csv'});actual=await files.download(key,{timeoutMs:10000,signal:context.signal,maxBytes:1024*1024});}
  if(actual.length!==bytes.length||jobHash(actual)!==jobHash(bytes))throw new Error('Report changed');
  const artifact={version:1,scope:files.scope,owner_id:input.owner_id,key,size_bytes:bytes.length,sha256:jobHash(bytes)};
  return pinned?{state:'mapped',email,report:artifact}:artifact;
}
try{await sdk.runJobTask(contract,handle,{timeoutMs:60000,store});}
catch(error){console.error(JSON.stringify({code:error.code??'handler_failed'}));process.exitCode=1;}
finally{await importer?.close();await sourceDB?.end();}
