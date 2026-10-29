import test from 'node:test';
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {once} from 'node:events';
import {AppsService,FaaSClient,type IssueTeam} from '../src/preview.js';

test('team roster, shared inbox and bulk assignment retain the team target',async(t)=>{
 const teamID='11111111-1111-4111-8111-111111111111';
 const member='22222222-2222-4222-8222-222222222222';
 const issue='33333333-3333-4333-8333-333333333333';
 const team:IssueTeam={id:teamID,key:'payments',name:'Payments',member_account_ids:[member],created_at:'2026-10-13T12:00:00Z',updated_at:'2026-10-13T12:00:00Z'};
 const server=createServer(async(req,res)=>{
  res.setHeader('Content-Type','application/json');
  const url=new URL(req.url!,'http://localhost');
  if(req.method==='GET'){
   assert.equal(url.pathname,'/v1/apps/payments/issues');assert.equal(url.searchParams.get('team'),teamID);assert.equal(url.searchParams.get('state'),'open');res.end(JSON.stringify({items:[]}));return;
  }
  const chunks:Buffer[]=[];for await(const chunk of req)chunks.push(Buffer.from(chunk));const body=JSON.parse(Buffer.concat(chunks).toString());
  if(req.method==='PUT') {assert.equal(url.pathname,'/v1/apps/payments/issue-teams/payments');assert.deepEqual(body,{name:'Payments',member_account_ids:[member]});res.end(JSON.stringify(team));}
  else {assert.equal(url.pathname,'/v1/apps/payments/issues/bulk-actions');assert.deepEqual(body,{issue_ids:[issue],action:{action:'assign',assignee_team_id:teamID}});res.end(JSON.stringify({items:[]}));}
 });
 server.listen(0,'127.0.0.1');await once(server,'listening');
 t.after(()=>new Promise<void>((resolve,reject)=>server.close(error=>error?reject(error):resolve())));
 const address=server.address();assert.ok(address && typeof address!=='string');
 new FaaSClient(`http://127.0.0.1:${address.port}`,{token:'test',retry:{maxAttempts:1,backoffMs:0}});
 assert.deepEqual(await AppsService.putIssueTeam({slug:'payments',teamKey:'payments',requestBody:{name:'Payments',member_account_ids:[member]}}),team);
 await AppsService.listIssues({slug:'payments',state:'open',team:teamID});
 await AppsService.bulkActOnIssues({slug:'payments',requestBody:{issue_ids:[issue],action:{action:'assign',assignee_team_id:teamID}}});
});
