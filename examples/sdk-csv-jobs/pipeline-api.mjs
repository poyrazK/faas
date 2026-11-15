// Serve the native code-first workflow entry. Application-authenticated start,
// inspect and live-ticket routes belong in the existing application backend.
import http from 'node:http';
import {pathToFileURL} from 'node:url';
import {resolve} from 'node:path';
import {createPipelineReviewServer} from './pipeline-review-api.mjs';
import {csvPipeline} from './pipeline.mjs';
const runtime=process.env.GREGALE_NODE_JOB_RUNTIME?pathToFileURL(resolve(process.env.GREGALE_NODE_JOB_RUNTIME)):new URL('../../sdk/node/dist/job-runtime.js',import.meta.url);
const sdk=await import(new URL('./preview.js',runtime)),{serveWorkflows,toNodeListener}=await import(new URL('./workflows.js',runtime));
const {default:pg}=await import('pg'),pool=new pg.Pool({connectionString:process.env.DATABASE_URL,statement_timeout:5000});
await pool.query('SELECT identity FROM public.gregale_pipeline_jobs LIMIT 0');
const scope=sdk.pipelineScope(JSON.parse(process.env.GREGALE_PIPELINE_SCOPE));
const client=sdk.createClient(scope.base_url,{token:process.env.FAAS_TOKEN});
const files=client.storage.files({appSlug:scope.app_slug,bucket:process.env.GREGALE_FILE_BUCKET});
const reviewed=process.env.GREGALE_REVIEWED_PIPELINE==='1',gateStore=sdk.postgresPipelineGateStore((sql,values)=>pool.query(sql,values));
if(reviewed)await pool.query('SELECT id FROM public.gregale_pipeline_gates LIMIT 0');
const {contract,pipeline,reviewContract}=await csvPipeline({sdk,client,scope,reviewed,gateStore,store:sdk.postgresPipelineJobStore((sql,values)=>pool.query(sql,values)),jobStore:await files.jobStore({timeoutMs:10000})});
const nativeEntry=toNodeListener(serveWorkflows([pipeline.definition]));
const server=reviewed?createPipelineReviewServer({sdk,client,scope,files,contract,pipeline,reviewContract,gateStore,nativeEntry,token:process.env.FAAS_TOKEN,users:JSON.parse(process.env.GREGALE_REVIEW_USERS??'{}'),realtime:JSON.parse(process.env.GREGALE_REVIEW_REALTIME??'null')}):http.createServer((request,response)=>{
  if(request.method==='POST'&&request.url==='/csv-pipeline')return nativeEntry(request,response);
  response.writeHead(404);response.end();
});
server.listen(Number(process.env.PORT??8080),process.env.HOST??'127.0.0.1');
process.on('SIGTERM',()=>server.close(()=>{void pool.end();}));
