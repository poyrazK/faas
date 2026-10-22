import test from 'node:test';
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {once} from 'node:events';
import {AppsService,FaaSClient,type IssueSplitPreview,type IssueSplitRule} from '../src/index.js';

test('priority preview and undo preserve eligible counts and reviewed destination',async(t)=>{
 const source='11111111-1111-4111-8111-111111111111';
 const rule='22222222-2222-4222-8222-222222222222';
 const target='33333333-3333-4333-8333-333333333333';
 const preview:IssueSplitPreview={original_issue_id:source,canonical_issue_id:source,matcher:{route:'/checkout'},lifetime_event_count:4,retained_event_count:3,
  matching_event_count:2,nonmatching_event_count:1,unavailable_history_event_count:1,matching_event_ids:[target],eligible_event_count:1,shadowed_event_count:1,eligible_event_ids:[target],application_status:'new-rule'};
 const disabled:IssueSplitRule={id:rule,source_issue_id:source,target_issue_id:target,priority:1,matcher:{route:'/checkout'},enabled:false,
  initial_moved_event_count:1,unavailable_history_event_count:1,created_at:'2026-10-11T12:00:00Z',updated_at:'2026-10-11T12:01:00Z'};
 const server=createServer(async(req,res)=>{
  assert.equal(req.method,'POST');
  const chunks:Buffer[]=[];for await(const chunk of req)chunks.push(Buffer.from(chunk));
  const body=JSON.parse(Buffer.concat(chunks).toString());
  res.setHeader('Content-Type','application/json');
  if(req.url===`/v1/apps/payments/issues/${source}/split-preview`){assert.deepEqual(body,{route:'/checkout'});res.end(JSON.stringify(preview));}
  else {assert.equal(req.url,`/v1/apps/payments/issues/${source}/split-rules/${rule}/undo`);assert.deepEqual(body,{canonical_source_issue_id:source});res.end(JSON.stringify(disabled));}
 });
 server.listen(0,'127.0.0.1');await once(server,'listening');
 t.after(()=>new Promise<void>((resolve,reject)=>server.close(error=>error?reject(error):resolve())));
 const address=server.address();assert.ok(address && typeof address!=='string');
 new FaaSClient(`http://127.0.0.1:${address.port}`,{token:'test',retry:{maxAttempts:1,backoffMs:0}});
 assert.deepEqual(await AppsService.previewIssueSplit({slug:'payments',issueId:source,requestBody:{route:'/checkout'}}),preview);
 assert.deepEqual(await AppsService.undoIssueSplitRule({slug:'payments',issueId:source,ruleId:rule,requestBody:{canonical_source_issue_id:source}}),disabled);
});
