import assert from 'node:assert/strict';
import test from 'node:test';
import { AppsService, ProjectsService, OpenAPI } from '../src/index.js';
test('verification lists preserve pagination and filters with GET only',async()=>{
 const old=globalThis.fetch,base=OpenAPI.BASE;OpenAPI.BASE='https://api.invalid';let calls=0;
 globalThis.fetch=async(input,init)=>{calls++;assert.equal(init?.method,'GET');const u=new URL(String(input));assert.equal(u.searchParams.get('limit'),'2');assert.equal(u.searchParams.get('cursor'),'a+/=');assert.equal(u.searchParams.get('deployment_id'),'22222222-2222-4222-8222-222222222222');if(u.pathname.includes('/apps/')){assert.equal(u.searchParams.get('include_members'),'true');assert.equal(u.searchParams.get('scope'),'production');}else{assert.equal(u.searchParams.has('include_members'),false);}return new Response(JSON.stringify({runs:[],observed_at:new Date().toISOString(),next_cursor:'next'}),{headers:{'Content-Type':'application/json'}});};
 try{const options={limit:2,cursor:'a+/=',deploymentId:'22222222-2222-4222-8222-222222222222',status:'pending' as const};const app=await AppsService.listBindingVerificationRuns({slug:'api',...options,scope:'production',includeMembers:true});assert.equal(app.next_cursor,'next');const graph=await ProjectsService.listProjectBindingVerificationRuns({slug:'project',environment:'production',...options});assert.deepEqual(graph.runs,[]);assert.equal(calls,2);}finally{globalThis.fetch=old;OpenAPI.BASE=base;}
});
