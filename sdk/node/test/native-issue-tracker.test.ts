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
 let revision=7,connected=true;
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
  res.setHeader('Content-Type','application/json');res.end(JSON.stringify(req.method==='GET'?{connected,revision,auth_method:'oauth',oauth_status:'reconnect_required',access_token_expires_at:'2026-10-14T12:00:00Z'}:{connected,revision}));
 });
 server.listen(0,'127.0.0.1');await once(server,'listening');t.after(()=>new Promise<void>((resolve,reject)=>server.close(error=>error?reject(error):resolve())));
 const address=server.address();assert.ok(address && typeof address!=='string');new FaaSClient(`http://127.0.0.1:${address.port}`,{token:'test',retry:{maxAttempts:1,backoffMs:0}});
 const initial=await AppsService.getIssueTrackerLinearConnection({slug:'payments'});assert.equal(initial.revision,7);assert.equal(initial.auth_method,'oauth');assert.equal(initial.oauth_status,'reconnect_required');assert.equal(initial.access_token_expires_at,'2026-10-14T12:00:00Z');
 const result=await AppsService.updateIssueTrackerLinearConnection({slug:'payments',requestBody:{connected:true,expected_revision:initial.revision,team_id:team,api_key:'secret'}});assert.equal(result.connected,true);
 const final=await AppsService.updateIssueTrackerLinearConnection({slug:'payments',requestBody:{connected:false,expected_revision:result.revision}});assert.deepEqual(final,{connected:false,revision:9});
});

test('native Linear creation preserves UUID recovery without a ticket URL',async(t)=>{
 const issue='33333333-3333-4333-8333-333333333333';
 const body={request_id:'11111111-1111-4111-8111-111111111111',team_id:'22222222-2222-4222-8222-222222222222',title:'Fix',recover:true};
 const server=createServer(async(req,res)=>{
  assert.equal(req.method,'POST');assert.equal(req.url,`/v1/apps/payments/issues/${issue}/native-linear-tickets`);
  const chunks:Buffer[]=[];for await(const chunk of req)chunks.push(Buffer.from(chunk));assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()),body);
  res.setHeader('Content-Type','application/json');res.end(JSON.stringify({request_id:body.request_id,state:'linked',ticket_url:'https://linear.app/acme/issue/PAY-42',link_id:'55555555-5555-4555-8555-555555555555'}));
 });
 server.listen(0,'127.0.0.1');await once(server,'listening');t.after(()=>new Promise<void>((resolve,reject)=>server.close(error=>error?reject(error):resolve())));
 const address=server.address();assert.ok(address && typeof address!=='string');new FaaSClient(`http://127.0.0.1:${address.port}`,{token:'test',retry:{maxAttempts:1,backoffMs:0}});
 const result=await AppsService.createNativeLinearIssueTicket({slug:'payments',issueId:issue,requestBody:body});assert.equal(result.state,'linked');assert.equal(result.request_id,body.request_id);
});

test('native Linear status keeps connection and group preview fences',async(t)=>{
 const issue='33333333-3333-4333-8333-333333333333',link='55555555-5555-4555-8555-555555555555';
 const preview={issue_id:issue,link_id:link,connection_revision:7,group_fingerprint:'a'.repeat(64),workspace_id:issue,team_id:link,viewer_id:issue,status:{provider:'linear' as const,url:'https://linear.app/acme/issue/PAY-42',id:issue,state:'resolved' as const,team_id:link,state_id:issue}};
 const body={desired_state:'open' as const,expected:preview.status,expected_connection_revision:7,expected_group_fingerprint:preview.group_fingerprint,linear_state_id:link};
 const server=createServer(async(req,res)=>{
  assert.equal(req.url,`/v1/apps/payments/issues/${issue}/tracker-links/${link}/native-linear-status`);
  if(req.method==='POST'){const chunks:Buffer[]=[];for await(const chunk of req)chunks.push(Buffer.from(chunk));assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()),body);}else assert.equal(req.method,'GET');
  res.setHeader('Content-Type','application/json');res.end(JSON.stringify(preview));
 });
 server.listen(0,'127.0.0.1');await once(server,'listening');t.after(()=>new Promise<void>((resolve,reject)=>server.close(error=>error?reject(error):resolve())));
 const address=server.address();assert.ok(address && typeof address!=='string');new FaaSClient(`http://127.0.0.1:${address.port}`,{token:'test',retry:{maxAttempts:1,backoffMs:0}});
 const result=await AppsService.getNativeLinearIssueTrackerStatus({slug:'payments',issueId:issue,linkId:link});assert.deepEqual(result,preview);
 assert.deepEqual(await AppsService.updateNativeLinearIssueTrackerStatus({slug:'payments',issueId:issue,linkId:link,requestBody:body}),preview);
});

test('Linear automatic policy preserves workflow mapping and native scope',async(t)=>{
 const issue='33333333-3333-4333-8333-333333333333',link='55555555-5555-4555-8555-555555555555';
 const body={direction:'both' as const,enabled:true,expected_revision:3,expected_issue_state:'open' as const,linear_open_state_id:'11111111-1111-4111-8111-111111111111',linear_resolved_state_id:'22222222-2222-4222-8222-222222222222',expected_linear_status:{issue_id:issue,link_id:link,connection_revision:7,group_fingerprint:'a'.repeat(64),workspace_id:issue,team_id:issue,viewer_id:issue,status:{provider:'linear' as const,url:'https://linear.app/acme/issue/PAY-42',id:issue,state:'open' as const,team_id:issue,state_id:issue}}};
 const server=createServer(async(req,res)=>{
  assert.equal(req.method,'PUT');assert.equal(req.url,`/v1/apps/payments/issues/${issue}/tracker-links/${link}/auto-sync`);
  const chunks:Buffer[]=[];for await(const chunk of req)chunks.push(Buffer.from(chunk));assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()),body);
  res.setHeader('Content-Type','application/json');res.end(JSON.stringify({direction:'both',observed_state:'resolved',link_id:link,source_issue_id:issue,enabled:true,revision:4,outcome:'matched',linear_open_state_id:body.linear_open_state_id,linear_resolved_state_id:body.linear_resolved_state_id}));
 });server.listen(0,'127.0.0.1');await once(server,'listening');t.after(()=>new Promise<void>((resolve,reject)=>server.close(error=>error?reject(error):resolve())));
 const address=server.address();assert.ok(address&&typeof address!=='string');new FaaSClient(`http://127.0.0.1:${address.port}`,{token:'test',retry:{maxAttempts:1,backoffMs:0}});
 const out=await AppsService.updateIssueTrackerAutoSync({slug:'payments',issueId:issue,linkId:link,requestBody:body});assert.equal(out.linear_resolved_state_id,body.linear_resolved_state_id);assert.equal(out.direction,'both');assert.equal(out.observed_state,'resolved');
});

test('Linear callback configuration keeps the signing secret write-only',async(t)=>{
 const body={connected:true,expected_revision:2,team_id:'22222222-2222-4222-8222-222222222222',api_key:'key',webhook_secret:'linear_signing_secret_1234567890abcdef'};
 const server=createServer(async(req,res)=>{
  assert.equal(req.method,'PUT');const chunks:Buffer[]=[];for await(const chunk of req)chunks.push(Buffer.from(chunk));assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()),body);
  res.setHeader('Content-Type','application/json');res.end(JSON.stringify({connected:true,revision:3,webhook_enabled:true,callback_path:'/hooks/issue-trackers/linear/app'}));
 });server.listen(0,'127.0.0.1');await once(server,'listening');t.after(()=>new Promise<void>((resolve,reject)=>server.close(error=>error?reject(error):resolve())));
 const address=server.address();assert.ok(address&&typeof address!=='string');new FaaSClient(`http://127.0.0.1:${address.port}`,{token:'test',retry:{maxAttempts:1,backoffMs:0}});
 const out=await AppsService.updateIssueTrackerLinearConnection({slug:'payments',requestBody:body});assert.equal(out.webhook_enabled,true);assert.equal(out.callback_path,'/hooks/issue-trackers/linear/app');assert.ok(!JSON.stringify(out).includes(body.webhook_secret));
});

test('frequency policies send the expected revision and preserve rule timing',async(t)=>{
 const rule={key:'burst',enabled:true,environment:'production',minimum_events:50,window_seconds:300,cooldown_seconds:1800};
 const server=createServer(async(req,res)=>{
  assert.equal(req.url,'/v1/apps/payments/issue-frequency-alert-policy');res.setHeader('Content-Type','application/json');
  if(req.method==='PUT'){
   const chunks:Buffer[]=[];for await(const chunk of req)chunks.push(Buffer.from(chunk));
   assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()),{expected_revision:7,rules:[rule]});
   res.end(JSON.stringify({revision:8,rules:[rule]}));return;
  }
  assert.equal(req.method,'GET');res.end(JSON.stringify({revision:7,rules:[rule]}));
 });
 server.listen(0,'127.0.0.1');await once(server,'listening');t.after(()=>new Promise<void>((resolve,reject)=>server.close(error=>error?reject(error):resolve())));
 const address=server.address();assert.ok(address&&typeof address!=='string');
 new FaaSClient(`http://127.0.0.1:${address.port}`,{token:'test',retry:{maxAttempts:1,backoffMs:0}});
 const current=await AppsService.getIssueFrequencyAlertPolicy({slug:'payments'});
 assert.deepEqual(current,{revision:7,rules:[rule]});
 const updated=await AppsService.setIssueFrequencyAlertPolicy({slug:'payments',requestBody:{expected_revision:current.revision,rules:[rule]}});
 assert.deepEqual(updated,{revision:8,rules:[rule]});
});
