import assert from 'node:assert/strict';
import test from 'node:test';
import { ProjectsService, OpenAPI } from '../src/index.js';
import type { CreateProjectBindingVerificationRunRequest } from '../src/index.js';
test('graph receipts preserve manifest, review and stable run routes', async () => {
 const oldFetch=globalThis.fetch,oldBase=OpenAPI.BASE;
 const id='11111111-1111-4111-8111-111111111111',target='22222222-2222-4222-8222-222222222222';
 const request:CreateProjectBindingVerificationRunRequest={id,ttl_seconds:60,object_storage_write:false,queue_publish:true,review_digests:{app:'a'.repeat(64)},manifest:{project:'project',environment:'production',ttl_seconds:60,deployments:{app:target},expected_active_release_id:'',expected_fallback_deployments:{},expected_policy_revisions:{app:3},service_targets:{app:{billing:target}}}};
 const calls:Array<[string,string]>=[];
 try {
  OpenAPI.BASE='https://api.invalid';
  globalThis.fetch=async(input,init)=>{const path=new URL(String(input)).pathname;calls.push([String(init?.method),path]);if(path.endsWith('/review'))return new Response(JSON.stringify({ready:true,manifest:request.manifest,review_digest:'b'.repeat(64),review_digests:request.review_digests}),{status:200,headers:{'Content-Type':'application/json'}});if(path.endsWith('/verification-runs'))assert.deepEqual(JSON.parse(String(init?.body)),request);return new Response(JSON.stringify({id,manifest:request.manifest,members:{app:{graph_run_id:id}}}),{status:200,headers:{'Content-Type':'application/json'}});};
  const review=await ProjectsService.reviewProjectBindingVerification({slug:'project',environment:'production',requestBody:{manifest:request.manifest,object_storage_write:false,queue_publish:true}});
  request.review_digest=review.review_digest;request.review_digests=review.review_digests;assert.equal(request.review_digest,'b'.repeat(64));
  const run=await ProjectsService.createProjectBindingVerificationRun({slug:'project',environment:'production',requestBody:request});assert.equal(run.manifest.service_targets.app?.billing,target);
  await ProjectsService.getProjectBindingVerificationRun({slug:'project',environment:'production',run:id});
  await ProjectsService.resumeProjectBindingVerificationRun({slug:'project',environment:'production',run:id});
  await ProjectsService.completeProjectBindingVerificationRun({slug:'project',environment:'production',run:id});
  await ProjectsService.cancelProjectBindingVerificationRun({slug:'project',environment:'production',run:id});
  const base=`/v1/projects/project/environments/production/release-sets/verification-runs/${id}`;
  assert.deepEqual(calls.slice(2),[['GET',base],['POST',base+'/resume'],['POST',base+'/complete'],['DELETE',base]]);
 }finally{globalThis.fetch=oldFetch;OpenAPI.BASE=oldBase;}
});
