import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { mkdtemp, mkdir, writeFile, rm, symlink, readFile } from 'node:fs/promises';
import { join, dirname, relative } from 'node:path';
import { tmpdir } from 'node:os';
import { gunzipSync } from 'node:zlib';
import { createClient, inspectProject, parseProjectPlan, validateProjectPlan, ProjectInspectionError, loadSourceDeploymentState, type ProjectInspectionOptions, type ProjectPlan } from '../src/index.js';
import { inspectProject as inspectPreviewProject } from '../src/preview.js';

const fixture=JSON.parse(readFileSync(new URL('../../../request-contracts/project-inspection.json',import.meta.url),'utf8')) as {cases:{name:string;files:Record<string,string>;directory:string;options:ProjectInspectionOptions;expected:Record<string,unknown>;fingerprint?:string}[]};
for(const [surface,inspect]of [['public',inspectProject],['preview',inspectPreviewProject]]as const)for(const item of fixture.cases)test(`${surface} project inspection: ${item.name}`,async t=>{
  const root=await mkdtemp(join(tmpdir(),'gregale-inspect-'));t.after(()=>rm(root,{recursive:true,force:true}));
  for(const [name,data]of Object.entries(item.files)){const path=join(root,name);await mkdir(dirname(path),{recursive:true});await writeFile(path,data);}
  const plan=await inspect(join(root,item.directory),item.options);
  for(const [key,value]of Object.entries(item.expected)){
    const actual=key==='errors'?plan.issues.filter(i=>i.severity==='error').map(i=>i.code):key==='warnings'?plan.issues.filter(i=>i.severity==='warning').map(i=>i.code):key==='context'?relative(plan.projectRoot,plan.contextRoot)||'.':key==='workspaceRoots'?plan.candidates.filter(i=>i.kind==='workspace').map(i=>i.root):key==='generatedPurposes'?plan.generatedFiles.map(i=>i.purpose):(plan as unknown as Record<string,unknown>)[key];
    assert.deepEqual(actual,value,`${item.name}: ${key}`);
  }
  assert.ok(Object.isFrozen(plan)&&Object.isFrozen(plan.settings));
  assert.deepEqual(parseProjectPlan(JSON.parse(JSON.stringify(plan))),plan);
  assert.ok(!JSON.stringify(plan).includes('private-value')&&!JSON.stringify(plan).includes('private-reference'));
  if(item.fingerprint)assert.equal(plan.fingerprint,item.fingerprint,'shared Node/Python fingerprint');
  if(!plan.issues.some(i=>i.severity==='error'))await validateProjectPlan(plan);
});

test('inspection detects stale inputs, rejects escapes, bounds work and honors cancellation',async t=>{
  const home=await mkdtemp(join(tmpdir(),'gregale-inspect-limits-'));t.after(()=>rm(home,{recursive:true,force:true}));
  const root=join(home,'project');await mkdir(root);await writeFile(join(root,'package.json'),'{}');
  const plan=await inspectProject(root);await writeFile(join(root,'package.json'),'{"name":"changed"}');
  await assert.rejects(validateProjectPlan(plan),(e:unknown)=>e instanceof ProjectInspectionError&&e.code==='project_changed');
  assert.throws(()=>parseProjectPlan({...plan,runtime:'changed'}));
  await assert.rejects(inspectProject(root,{maxEntries:0}),ProjectInspectionError);
  await writeFile(join(root,'README.md'),'source');
  await assert.rejects(inspectProject(root,{maxEntries:1}),ProjectInspectionError);
  await assert.rejects(inspectProject(root,{timeoutMs:0.001}),ProjectInspectionError);
  await assert.rejects(inspectProject(root,{signal:AbortSignal.abort()}));
  await writeFile(join(root,'gregale.yaml'),'x'.repeat(1024*1024+1));
  await assert.rejects(inspectProject(root),ProjectInspectionError);
  await rm(join(root,'gregale.yaml'));await writeFile(join(home,'outside'),'private-value');
  await symlink(join(home,'outside'),join(root,'gregale.yaml'));
  const escaped=await inspectProject(root);assert.ok(escaped.issues.some(i=>i.code==='unsafe_path'));
  await rm(join(root,'gregale.yaml'));await mkdir(join(home,'outside-dir'));await writeFile(join(home,'outside-dir','handler.js'),'private-value');await symlink(join(home,'outside-dir'),join(root,'fn'));
  await writeFile(join(root,'gregale.yaml'),'functions:\n  - {name: one, root: fn}\n');const escapedFunction=await inspectProject(root);assert.ok(escapedFunction.issues.some(i=>i.code==='unsafe_path'));parseProjectPlan(escapedFunction);
});

function tarFiles(bytes:Buffer):Record<string,string>{
  const tar=gunzipSync(bytes),files:Record<string,string>={};
  for(let offset=0;offset+512<=tar.length;){const header=tar.subarray(offset,offset+512),name=header.subarray(0,100).toString().replace(/\0.*$/s,'');if(!name)break;const size=parseInt(header.subarray(124,136).toString().replace(/\0.*$/s,'').trim()||'0',8);if(header[156]===48||header[156]===0)files[name]=tar.subarray(offset+512,offset+512+size).toString();offset+=512+Math.ceil(size/512)*512;}return files;
}
for(const language of ['js','go'])test(`project workflow: virtual ${language} files, binding and source-free resume`,async t=>{
  const home=await mkdtemp(join(tmpdir(),'gregale-project-flow-'));t.after(()=>rm(home,{recursive:true,force:true}));
  const root=join(home,'project'),source=join(root,'functions','one');await mkdir(source,{recursive:true});await writeFile(join(root,'gregale.yaml'),'functions:\n  - {name: one, root: functions/one}\n');await writeFile(join(source,'handler.'+language),'// source\n');
  const project=await inspectProject(root,{appSlug:'api'}),statePath=join(home,'state.json');assert.deepEqual(project.issues,[]);
  let requests=0,received=Buffer.alloc(0),receipt:Record<string,string>|undefined;const id='11111111-1111-4111-8111-111111111111';
  const client=createClient('https://project.test',{fetch:async(url,init)=>{requests++;const path=new URL(String(url)).pathname;
    if(init!.method==='POST'&&path==='/v1/uploads')return Response.json({upload_id:'upload',chunk_size:65536,total_size:JSON.parse(init!.body as string).total_size,expires_at:'2026-11-20T00:00:00Z'},{status:201});
    if(init!.method==='GET'&&path==='/v1/uploads/upload')return Response.json({upload_id:'upload',app_slug:'api',chunk_size:65536,total_size:(await loadSourceDeploymentState(statePath)).archive!.size,received_bytes:received.length,status:'open',expires_at:'2026-11-20T00:00:00Z'});
    if(init!.method==='PATCH'){received=Buffer.concat([received,Buffer.from(await(init!.body as Blob).arrayBuffer())]);return new Response(undefined,{headers:{'Upload-Offset':String(received.length)}});}
    if(path.includes('/deployment-submissions/'))return Response.json(receipt??{code:'not_found'},{status:receipt?200:404});
    if(init!.method==='POST'&&path.endsWith('/commit')){receipt={attempt_id:(await loadSourceDeploymentState(statePath)).upload!.attempt_id,app_id:id,deployment_id:id,accepted_at:'2026-11-14T17:00:00Z'};return Response.json({id},{status:201});}
    if(path.endsWith('/timeline'))return Response.json({deployment_id:id,app:'api',status:'live',phase:'released',summary:'test',traffic_percent:100,events:[],created_at:'2026-11-14T17:00:00Z'});
    throw new Error('Unexpected request '+path);
  }});
  let paused=false;
  await assert.rejects(client.deployments.deployProject(project,{statePath,timeoutMs:5000,onProgress:event=>{if(!paused&&event.phase==='uploading'&&!event.upload){paused=true;throw new Error('pause after preparation');}}}));
  assert.ok(paused);assert.equal(requests,0);
  const saved=await loadSourceDeploymentState(statePath),archive=await readFile(join(home,`.gregale-deploy-${saved.workflow_id}.tar.gz`));
  const files=tarFiles(archive);assert.deepEqual(JSON.parse(files['gregale.yaml']!),{function:{runtime:language==='js'?'node22':'go124',handler:'handler.handler'}});
  if(language==='go')assert.equal(files['go.mod'],'module gregale-function\n\ngo 1.26.11\n');
  await assert.rejects(readFile(join(source,'gregale.yaml')));await assert.rejects(readFile(join(source,'go.mod')));
  const changed=await inspectProject(root,{appSlug:'api',runtime:language==='js'?'node24':'go124-alpine'});
  await assert.rejects(client.deployments.resumeProjectDeployment(changed,{statePath,timeoutMs:5000}));assert.equal(requests,0);
  await rm(root,{recursive:true});const roundtrip:ProjectPlan=parseProjectPlan(JSON.parse(JSON.stringify(project)));
  const result=await client.deployments.resumeProjectDeployment(roundtrip,{statePath,timeoutMs:5000});assert.equal(result.outcome,'succeeded');assert.deepEqual(received,archive);
});

test('project workflows reject stale plans and excluded build inputs before HTTP',async t=>{
  const home=await mkdtemp(join(tmpdir(),'gregale-project-stale-'));t.after(()=>rm(home,{recursive:true,force:true}));const root=join(home,'project');await mkdir(root);await writeFile(join(root,'package.json'),'{}');
  const project=await inspectProject(root,{appSlug:'api'}),client=createClient('https://project.test',{fetch:async()=>{assert.fail('HTTP before validation');}});
  await writeFile(join(root,'package.json'),'{"name":"changed"}');await assert.rejects(client.deployments.deployProject(project,{statePath:join(home,'stale.json'),timeoutMs:5000}));
  const fresh=await inspectProject(root,{appSlug:'api'});await assert.rejects(client.deployments.deployProject(fresh,{statePath:join(home,'excluded.json'),timeoutMs:5000,preparation:{exclude:['package.json']}}));
  await assert.rejects(client.deployments.deploySource({directory:fresh.contextRoot,appSlug:'api',deployOptions:{source_root:'.',runtime:'node22'}},{project:fresh,statePath:join(home,'metadata.json'),timeoutMs:5000}));
});

test('generated project files honor ignore rules and source limits',async t=>{
  const home=await mkdtemp(join(tmpdir(),'gregale-project-generated-'));t.after(()=>rm(home,{recursive:true,force:true}));const root=join(home,'project');await mkdir(root);await writeFile(join(root,'handler.go'),'// source\n');
  const client=createClient('https://project.test',{fetch:async()=>{assert.fail('HTTP before validation');}});
  for(const [name,preparation]of [['exclude',{exclude:['go.mod']}],['entries',{maxEntries:1}],['bytes',{maxBytes:10}]]as const){const plan=await inspectProject(root,{appSlug:'api'});await assert.rejects(client.deployments.deployProject(plan,{statePath:join(home,name+'.json'),timeoutMs:5000,preparation}));}
  await writeFile(join(root,'.gregaleignore'),'go.mod\n');const plan=await inspectProject(root,{appSlug:'api'});await assert.rejects(client.deployments.deployProject(plan,{statePath:join(home,'ignored.json'),timeoutMs:5000}));
});
