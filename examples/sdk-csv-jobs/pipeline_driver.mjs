// Protocol adapter for local process-restart acceptance. A deployed app serves
// pipeline.definition through serveWorkflows(), which checks gateway identity.
import {readFile} from 'node:fs/promises';
import {pathToFileURL} from 'node:url';
import {resolve} from 'node:path';
import {csvPipeline} from './pipeline.mjs';
const runtime=process.env.GREGALE_NODE_JOB_RUNTIME?pathToFileURL(resolve(process.env.GREGALE_NODE_JOB_RUNTIME)):new URL('../../sdk/node/dist/job-runtime.js',import.meta.url);
const sdk=await import(new URL('./preview.js',runtime)),{runWorkflowInvocation}=await import(new URL('./workflows.js',runtime));
const {default:pg}=await import('pg'),pool=new pg.Pool({connectionString:process.env.DATABASE_URL,statement_timeout:5000});
const client=sdk.createClient(process.env.FAAS_API,{token:process.env.FAAS_TOKEN}),files=client.storage.files({appSlug:'imports',bucket:process.env.GREGALE_FILE_BUCKET});
try{
  const query=async(sql,values)=>{
    const result=await pool.query(sql,values);
    // Test-only barrier: both language processes capture an absent intent
    // before either may reserve it. This adapter is not the production entry.
    if(process.env.GREGALE_PIPELINE_BARRIER&&sql.startsWith('SELECT receipt FROM')){
      await pool.query("INSERT INTO sdk_pipeline_barrier VALUES ('node') ON CONFLICT DO NOTHING");
      for(let i=0;i<1000;i++){if((await pool.query("SELECT count(*)::int AS n FROM sdk_pipeline_barrier WHERE driver IN ('node','python')")).rows[0].n===2)return result;await new Promise(r=>setTimeout(r,5));}
      throw new Error('Fixture barrier expired');
    }return result;
  };
  const scope=JSON.parse(process.env.GREGALE_PIPELINE_SCOPE),store=sdk.postgresPipelineJobStore(query),jobStore=await files.jobStore({timeoutMs:10000});
  const {pipeline}=await csvPipeline({sdk,client,scope,store,jobStore});
  // Wait for both module imports before starting the SDK's operation deadline.
  if(process.env.GREGALE_PIPELINE_BARRIER){
    await pool.query("INSERT INTO sdk_pipeline_barrier VALUES ('node-ready') ON CONFLICT DO NOTHING");
    let ready=false;
    for(let i=0;i<4000;i++){if((await pool.query("SELECT count(*)::int AS n FROM sdk_pipeline_barrier WHERE driver IN ('node-ready','python-ready')")).rows[0].n===2){ready=true;break;}await new Promise(r=>setTimeout(r,5));}
    if(!ready)throw new Error('Fixture driver startup expired');
  }
  const request=JSON.parse(await readFile(process.argv[2],'utf8'));
  console.log(JSON.stringify(await runWorkflowInvocation(pipeline.definition,request)));
}finally{await pool.end();}
