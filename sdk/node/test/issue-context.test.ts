import test from 'node:test';
import assert from 'node:assert/strict';
import {createIssueContext,issueTagKeys} from '../src/issue-context.js';
import {createIssueReporter} from '../src/issues.js';

test('captured context is an immutable retry snapshot rather than a live operation buffer',async()=>{
  const context=createIssueContext();context.setTag('component','payments');context.addBreadcrumb({message:'starting charge',category:'workflow'});
  const requests:Array<Record<string,unknown>>=[];
  const reporter=createIssueReporter({baseURL:'https://api.gregale.dev',app:'payments',token:'g_issue_test',
    fetch:(async(_url,options)=>{requests.push(JSON.parse(String(options?.body)));return new Response('{}',{status:requests.length===1?503:202});}) as typeof fetch});
  reporter.captureException(new Error('payment failed'),context.snapshot());
  context.setTag('component','other-request');context.addBreadcrumb({message:'not part of this error'});
  assert.equal(await reporter.flush(),false);assert.equal(await reporter.flush(),true);
  assert.deepEqual(requests[0],requests[1]);assert.equal((requests[0]?.tags as Record<string,string>).component,'payments');
  assert.equal((requests[0]?.breadcrumbs as unknown[]).length,1);await reporter.close();
});

test('request-local context keeps a bounded timeline and immutable snapshots',()=>{
  const context=createIssueContext({clock:()=>new Date('2026-10-13T12:00:00Z')});
  assert.equal(context.setTag('component','payments'),true);
  for(let i=0;i<20;i++)assert.equal(context.addBreadcrumb({message:'step '+i,category:'workflow'}),true);
  const snapshot=context.snapshot();assert.equal(snapshot.breadcrumbs.length,16);assert.equal(snapshot.breadcrumbs_dropped,4);
  assert.equal(snapshot.breadcrumbs[0]?.message,'step 4');
  context.addBreadcrumb({message:'another operation'});context.setTag('component','exports');
  assert.equal(snapshot.breadcrumbs.at(-1)?.message,'step 19');assert.equal(snapshot.tags.component,'payments');
  assert.equal(createIssueContext().snapshot().breadcrumbs.length,0);
});
test('context rejects unsafe tag keys and preserves UTF-8 bounds without arbitrary event data',()=>{
  const context=createIssueContext();
  assert.equal(context.setTag('__proto__','value'),false);assert.equal(context.setTag('constructor','value'),false);
  context.setTag('component','😀'.repeat(100));context.addBreadcrumb({message:'😀'.repeat(100)});
  const snapshot=context.snapshot();assert.equal(Buffer.byteLength(snapshot.tags.component!),128);
  assert.equal(Buffer.byteLength(snapshot.breadcrumbs[0]!.message),256);
  assert.equal(snapshot.tags.component!.includes('�'),false);assert.equal(snapshot.breadcrumbs[0]!.message.includes('�'),false);
  for(const key of issueTagKeys)assert.equal(context.setTag(key,'value'),true);
  assert.equal(context.setTag('overflow','value'),false);assert.equal(context.setTag('component','updated'),true);
});
