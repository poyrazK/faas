import assert from 'node:assert/strict';
import test from 'node:test';
import { AppsService,ProjectsService,OpenAPI } from '../src/preview.js';
import type {BindingVerificationRunDiagnostics} from '../src/preview.js';
test('typed diagnostics preserve public changes, blockers and recovery through read-only routes',async()=>{
 const oldFetch=globalThis.fetch,oldBase=OpenAPI.BASE,id='11111111-1111-4111-8111-111111111111';const calls:string[]=[];
 const member:BindingVerificationRunDiagnostics={budget:{run_id:id,max_concurrency:1,active_resources:1,waiting_steps:2,full:true},run_id:id,app:'api',deployment_id:'22222222-2222-4222-8222-222222222222',scope:'production',status:'pending',checked_at:'2026-10-14T00:00:00Z',expires_at:'2026-10-14T00:01:00Z',complete:true,check_passed:false,blockers:[{code:'credential_generation_changed',message:'Generation changed.',recovery:'review_new_run',type:'object_storage',binding:'ASSETS'}],changes:[{field:'credential_generation',reviewed:'1',current:'2'}],recovery:[{action:'review_new_run',message:'Review a new run.'}]};
 try{OpenAPI.BASE='https://api.invalid';globalThis.fetch=async(input,init)=>{const path=new URL(String(input)).pathname;assert.equal(init?.method,'GET');calls.push(path);return new Response(JSON.stringify(path.startsWith('/v1/apps/')?member:{budget:member.budget,run_id:id,project:'project',environment:'production',status:'pending',checked_at:member.checked_at,expires_at:member.expires_at,complete:true,members:{api:member},blockers:member.blockers,changes:member.changes,recovery:member.recovery}),{status:200,headers:{'Content-Type':'application/json'}});};
 const app=await AppsService.getBindingVerificationRunDiagnostics({slug:'api',run:id});assert.equal(app.budget?.max_concurrency,1);assert.equal(app.budget?.full,true);assert.equal(app.check_passed,false);assert.equal(app.changes[0]?.current,'2');
 const graph=await ProjectsService.getProjectBindingVerificationRunDiagnostics({slug:'project',environment:'production',run:id});assert.equal(graph.budget?.run_id,id);assert.equal(graph.members.api?.run_id,id);assert.equal(graph.recovery[0]?.action,'review_new_run');
 assert.deepEqual(calls,[`/v1/apps/api/bindings/verification-runs/${id}/diagnostics`,`/v1/projects/project/environments/production/release-sets/verification-runs/${id}/diagnostics`]);
 }finally{globalThis.fetch=oldFetch;OpenAPI.BASE=oldBase;}
});
