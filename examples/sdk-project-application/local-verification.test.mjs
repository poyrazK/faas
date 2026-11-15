import test from 'node:test';
import assert from 'node:assert/strict';
import {spawn, execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {createServer} from 'node:http';
import {mkdtemp,cp,symlink,writeFile,rm,access} from 'node:fs/promises';
import {join,resolve} from 'node:path';
import {tmpdir} from 'node:os';
import {fileURLToPath} from 'node:url';

const exec=promisify(execFile), here=fileURLToPath(new URL('.',import.meta.url));
const repo=resolve(here,'../..'), pgBin=process.env.GREGALE_LOCAL_PG_BIN??'/opt/homebrew/opt/postgresql@16/bin';
const python=process.env.GREGALE_LOCAL_PYTHON??resolve(repo,'sdk/python/.venv/bin/python');
async function eventually(work) {
  const end=Date.now()+15000;
  while(Date.now()<end){try{if(await work())return;}catch{}await new Promise(r=>setTimeout(r,100));}
  assert.fail('Local example did not become ready within 15 seconds');
}
async function stop(child) {
  if(child.exitCode!==null)return;
  const exited=new Promise(r=>child.once('exit',r));child.kill('SIGTERM');
  const timer=setTimeout(()=>child.kill('SIGKILL'),5000);try{await exited;}finally{clearTimeout(timer);}
}

// This test uses real PostgreSQL, API and worker processes. The control-plane
// responses are fixtures; deployment/ownership acceptance is the deployed Go journey.
test('both SDKs verify actual worker-written PostgreSQL data through failure and correction',{timeout:120000},async t=>{
  try{await access(join(pgBin,'initdb'));await access(python);}catch{t.skip('Set GREGALE_LOCAL_PG_BIN and GREGALE_LOCAL_PYTHON');return;}
  const home=await mkdtemp(join(tmpdir(),'gregale-verification-'));const cleanups=[];const cleanup=fn=>cleanups.push(fn);t.after(async()=>{try{for(const fn of cleanups.reverse())await fn();}finally{await rm(home,{recursive:true,force:true});}});
  await cp(join(here,'source'),join(home,'source'),{recursive:true,filter:path=>!path.includes('node_modules')});
  await symlink(resolve(repo,'sdk/node/node_modules'),join(home,'source/node_modules'));
  await exec(join(pgBin,'initdb'),['-D',join(home,'pg'),'-U','sdk','-A','trust','--no-locale']);
  const postgres=spawn(join(pgBin,'postgres'),['-D',join(home,'pg'),'-k',home,'-h',''],{stdio:'ignore'});cleanup(()=>stop(postgres));
  const database=`postgresql://sdk@localhost/postgres?host=${encodeURIComponent(home)}`;
  const {default:pg}=await import(new URL('../../sdk/node/node_modules/pg/lib/index.js',import.meta.url));
  const pool=new pg.Pool({connectionString:database,connectionTimeoutMillis:1000});cleanup(()=>pool.end());
  await eventually(async()=>{await pool.query('SELECT 1');return true;});
  const target={version:1,base_url:'',project_slug:'shop',environment:null,workloads:[
    {slug:'shop-api',app_id:'1'.repeat(32),deployment_id:'a'.repeat(32)},
    {slug:'shop-worker',app_id:'2'.repeat(32),deployment_id:'b'.repeat(32)},
  ]};
  const control=createServer((request,response)=>{
    assert.equal(request.method,'GET');assert.equal(request.headers.authorization,'Bearer local-verifier-fixture');
    const path=new URL(request.url,'http://localhost').pathname,w=target.workloads.find(w=>path.includes(w.slug)||path.includes(w.deployment_id));
    const date=new Date(Date.now()-10).toISOString();let data;
    if(path==='/v1/projects/shop')data={id:'3'.repeat(32),slug:'shop'};
    else if(path.endsWith('/health'))data={app_id:w.app_id,scope:'default',status:'healthy',phase:w.slug.endsWith('worker')?'unsupported':'serving',evaluated_at:date,metrics_as_of:date,valid_for_seconds:60,serving_deployment_ids:[w.deployment_id]};
    else if(path.endsWith('/timeline'))data={deployment_id:w.deployment_id,app:w.slug,status:'live',phase:'released',traffic_percent:100,events:[]};
    else if(path.startsWith('/v1/deployments/'))data={id:w.deployment_id,app_id:w.app_id,status:'live'};
    else data={id:w.app_id,slug:w.slug,project_id:'3'.repeat(32),workload_class:w.slug.endsWith('worker')?'worker':'http'};
    response.writeHead(200,{'Content-Type':'application/json'});response.end(JSON.stringify(data));
  });
  await new Promise(r=>control.listen(0,'127.0.0.1',r));cleanup(()=>new Promise(r=>control.close(r)));
  target.base_url=`http://127.0.0.1:${control.address().port}`;
  const reserved=createServer();await new Promise(r=>reserved.listen(0,'127.0.0.1',r));const port=reserved.address().port;await new Promise(r=>reserved.close(r));
  const env={...process.env,DATABASE_URL:database,PORT:String(port),FAAS_DEPLOYMENT_ID:target.workloads[0].deployment_id};
  const api=spawn(process.execPath,[join(home,'source/api.mjs')],{env,stdio:'ignore'});cleanup(()=>stop(api));
  const applicationURL=`http://127.0.0.1:${port}`;
  let worker=spawn(process.execPath,[join(home,'source/worker.mjs')],{env:{...env,FAAS_DEPLOYMENT_ID:target.workloads[1].deployment_id},stdio:'ignore'});cleanup(()=>stop(worker));
  const verificationEnv={...process.env,FAAS_API:target.base_url,FAAS_TOKEN:'local-verifier-fixture',GREGALE_PROJECT_STATE:home,GREGALE_APPLICATION_URL:applicationURL,PYTHONPATH:resolve(repo,'sdk/python')};
  const verify=async(expected)=>{
    await writeFile(join(home,'verification-target.json'),JSON.stringify(target),{mode:0o600});
    for(const [binary,driver]of [[process.execPath,'verify.mjs'],[python,'verify.py']]){
      let result;try{result=await exec(binary,[join(here,driver)],{env:verificationEnv,timeout:15000});}catch(error){result=error;}
      const report=JSON.parse(result.stdout);assert.equal(report.outcome,expected);
      if(expected==='failed'){assert.equal(report.workloads[0].outcome,'verified');assert.equal(report.workloads[1].code,'smoke_failed');}
    }
  };
  const heartbeat=async status=>eventually(async()=>{const response=await fetch(`${applicationURL}?worker_deployment_id=${target.workloads[1].deployment_id}`);if(!response.ok)return false;const body=await response.json();return body.worker?.status===status&&body.worker.deployment_id===target.workloads[1].deployment_id;});
  await heartbeat('processed');await verify('verified');
  await stop(worker);target.workloads[1].deployment_id='c'.repeat(32);
  await writeFile(join(home,'source/worker-fault.json'),'{"fail":true}');
  worker=spawn(process.execPath,[join(home,'source/worker.mjs')],{env:{...env,FAAS_DEPLOYMENT_ID:target.workloads[1].deployment_id},stdio:'ignore'});
  await heartbeat('failed');await verify('failed');
  await stop(worker);target.workloads[1].deployment_id='d'.repeat(32);
  await writeFile(join(home,'source/worker-fault.json'),'{"fail":false}');
  worker=spawn(process.execPath,[join(home,'source/worker.mjs')],{env:{...env,FAAS_DEPLOYMENT_ID:target.workloads[1].deployment_id},stdio:'ignore'});
  await heartbeat('processed');await verify('verified');
});
