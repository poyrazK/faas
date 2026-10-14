import test from 'node:test';
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {once} from 'node:events';
import {AppsService,FaaSClient,type NativeIssueTrackerStatus} from '../src/index.js';

test('native status API retains the provider snapshot without provider credentials',async(t)=>{
 const issue='33333333-3333-4333-8333-333333333333';const link='55555555-5555-4555-8555-555555555555';
 const observed:NativeIssueTrackerStatus={issue_id:issue,link_id:link,installation_id:42,repository:'owner/repo',repository_id:100,status:{provider:'github',url:'https://github.com/owner/repo/issues/42',id:'42',state:'resolved'}};
 let writes=0;
 const server=createServer(async(req,res)=>{
  assert.equal(req.url,`/v1/apps/payments/issues/${issue}/tracker-links/${link}/native-status`);res.setHeader('Content-Type','application/json');
  if(req.method==='POST') {writes++;const chunks:Buffer[]=[];for await(const chunk of req)chunks.push(Buffer.from(chunk));assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()),{desired_state:'open',expected:observed.status});res.end(JSON.stringify({...observed,status:{...observed.status,state:'open'}}));return;}
  assert.equal(req.method,'GET');res.end(JSON.stringify(observed));
 });
 server.listen(0,'127.0.0.1');await once(server,'listening');t.after(()=>new Promise<void>((resolve,reject)=>server.close(error=>error?reject(error):resolve())));
 const address=server.address();assert.ok(address && typeof address!=='string');new FaaSClient(`http://127.0.0.1:${address.port}`,{token:'test',retry:{maxAttempts:1,backoffMs:0}});
 assert.deepEqual(await AppsService.getNativeIssueTrackerStatus({slug:'payments',issueId:issue,linkId:link}),observed);
 const result=await AppsService.updateNativeIssueTrackerStatus({slug:'payments',issueId:issue,linkId:link,requestBody:{desired_state:'open',expected:observed.status}});assert.equal(result.status.state,'open');assert.equal(writes,1);
});

test('native creation keeps the stable request identity and recovery URL',async(t)=>{
 const issue='33333333-3333-4333-8333-333333333333';
 const body={request_id:'11111111-1111-4111-8111-111111111111',repository:'owner/repo',title:'Fix',recovery_url:'https://github.com/owner/repo/issues/42'};
 const server=createServer(async(req,res)=>{
  assert.equal(req.method,'POST');assert.equal(req.url,`/v1/apps/payments/issues/${issue}/native-tickets`);
  const chunks:Buffer[]=[];for await(const chunk of req)chunks.push(Buffer.from(chunk));assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()),body);
  res.setHeader('Content-Type','application/json');res.end(JSON.stringify({request_id:body.request_id,state:'linked',ticket_url:body.recovery_url,link_id:'55555555-5555-4555-8555-555555555555'}));
 });
 server.listen(0,'127.0.0.1');await once(server,'listening');t.after(()=>new Promise<void>((resolve,reject)=>server.close(error=>error?reject(error):resolve())));
 const address=server.address();assert.ok(address && typeof address!=='string');new FaaSClient(`http://127.0.0.1:${address.port}`,{token:'test',retry:{maxAttempts:1,backoffMs:0}});
 const result=await AppsService.createNativeIssueTicket({slug:'payments',issueId:issue,requestBody:body});assert.equal(result.state,'linked');assert.equal(result.request_id,body.request_id);
});

test('automatic sync uses a known policy revision and aligned native snapshot',async(t)=>{
 const issue='33333333-3333-4333-8333-333333333333',link='55555555-5555-4555-8555-555555555555';
 const status:NativeIssueTrackerStatus={issue_id:issue,link_id:link,installation_id:42,repository:'owner/repo',repository_id:100,status:{provider:'github',url:'https://github.com/owner/repo/issues/42',id:'42',state:'open'}};
 const body={enabled:true,expected_revision:7,expected_status:status,expected_issue_state:'open' as const};
 const server=createServer(async(req,res)=>{
  assert.equal(req.url,`/v1/apps/payments/issues/${issue}/tracker-links/${link}/auto-sync`);
  if(req.method==='PUT'){const chunks:Buffer[]=[];for await(const chunk of req)chunks.push(Buffer.from(chunk));assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()),body);}else{assert.equal(req.method,'GET');}
  res.setHeader('Content-Type','application/json');res.end(JSON.stringify({link_id:link,enabled:req.method==='PUT',revision:8,source_issue_id:issue,outcome:'matched'}));
 });server.listen(0,'127.0.0.1');await once(server,'listening');t.after(()=>new Promise<void>((resolve,reject)=>server.close(error=>error?reject(error):resolve())));
 const address=server.address();assert.ok(address && typeof address!=='string');new FaaSClient(`http://127.0.0.1:${address.port}`,{token:'test',retry:{maxAttempts:1,backoffMs:0}});
 await AppsService.getIssueTrackerAutoSync({slug:'payments',issueId:issue,linkId:link});
 const out=await AppsService.updateIssueTrackerAutoSync({slug:'payments',issueId:issue,linkId:link,requestBody:body});assert.equal(out.enabled,true);assert.equal(out.revision,8);
});

test('Linear connections preserve revision and send credentials only on connect', async(t)=>{
 let revision=7,connected=false;
 const team='22222222-2222-4222-8222-222222222222';
 const server=createServer(async(req,res)=>{
  assert.equal(req.url,'/v1/apps/payments/issue-tracker-linear-connection');
  if(req.method==='PUT'){
   const chunks:Buffer[]=[];for await(const chunk of req)chunks.push(Buffer.from(chunk));
   const body=JSON.parse(Buffer.concat(chunks).toString());
   assert.equal(body.expected_revision,revision);
   if(body.connected)assert.deepEqual(body,{connected:true,expected_revision:7,team_id:team,api_key:'secret'});
   else assert.deepEqual(body,{connected:false,expected_revision:8});
   connected=body.connected;revision++;
  }else assert.equal(req.method,'GET');
  res.setHeader('Content-Type','application/json');res.end(JSON.stringify({connected,revision}));
 });
 server.listen(0,'127.0.0.1');await once(server,'listening');t.after(()=>new Promise<void>((resolve,reject)=>server.close(error=>error?reject(error):resolve())));
 const address=server.address();assert.ok(address && typeof address!=='string');new FaaSClient(`http://127.0.0.1:${address.port}`,{token:'test',retry:{maxAttempts:1,backoffMs:0}});
 const initial=await AppsService.getIssueTrackerLinearConnection({slug:'payments'});assert.equal(initial.revision,7);
 const result=await AppsService.updateIssueTrackerLinearConnection({slug:'payments',requestBody:{connected:true,expected_revision:initial.revision,team_id:team,api_key:'secret'}});assert.equal(result.connected,true);
 const final=await AppsService.updateIssueTrackerLinearConnection({slug:'payments',requestBody:{connected:false,expected_revision:result.revision}});assert.deepEqual(final,{connected:false,revision:9});
});
