import test from 'node:test';
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {once} from 'node:events';
import {AppsService,FaaSClient,type IssueTrackerLink} from '../src/preview.js';

test('tracker references retain identity and pagination through SDK management',async(t)=>{
 const issue='11111111-1111-4111-8111-111111111111';
 const link:IssueTrackerLink={id:'22222222-2222-4222-8222-222222222222',original_issue_id:issue,provider:'github',external_key:'owner/repo#42',url:'https://github.com/owner/repo/issues/42',title:'Fix checkout',created_at:'2026-10-14T12:00:00Z',updated_at:'2026-10-14T12:00:00Z'};
 let calls=0;
 const server=createServer(async(req,res)=>{
  calls++;res.setHeader('Content-Type','application/json');
  const url=new URL(req.url!,'http://localhost');
  const base=`/v1/apps/payments/issues/${issue}/tracker-links`;
  if(req.method==='GET') {assert.equal(url.pathname,base);assert.equal(url.searchParams.get('cursor'),'next+/=');res.end(JSON.stringify({items:[link],next_cursor:'tail'}));return;}
  if(req.method==='DELETE') {assert.equal(url.pathname,`${base}/${link.id}`);res.writeHead(204);res.end();return;}
  assert.equal(req.method,'POST');assert.equal(url.pathname,base);
  const chunks:Buffer[]=[];for await(const chunk of req)chunks.push(Buffer.from(chunk));
  assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()),{url:link.url,title:link.title});res.end(JSON.stringify(link));
 });
 server.listen(0,'127.0.0.1');await once(server,'listening');
 t.after(()=>new Promise<void>((resolve,reject)=>server.close(error=>error?reject(error):resolve())));
 const address=server.address();assert.ok(address && typeof address!=='string');
 new FaaSClient(`http://127.0.0.1:${address.port}`,{token:'test',retry:{maxAttempts:1,backoffMs:0}});
 assert.deepEqual(await AppsService.putIssueTrackerLink({slug:'payments',issueId:issue,requestBody:{url:link.url,title:link.title}}),link);
 assert.deepEqual(await AppsService.listIssueTrackerLinks({slug:'payments',issueId:issue,cursor:'next+/='}),{items:[link],next_cursor:'tail'});
 await AppsService.deleteIssueTrackerLink({slug:'payments',issueId:issue,linkId:link.id});assert.equal(calls,3);
});
