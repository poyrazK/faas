import test from 'node:test';
import assert from 'node:assert/strict';
import { createHmac } from 'node:crypto';
import { formatIssueSlackMessage, issueSlackMessageFromWebhook, WebhookVerificationError } from '../src/preview.js';

const app='11111111-1111-4111-8111-111111111111';
const team='22222222-2222-4222-8222-222222222222';
const issue='33333333-3333-4333-8333-333333333333';
const activity='44444444-4444-4444-8444-444444444444';
const options={appID:app,appSlug:'payments',dashboardOrigin:'https://gregale.dev'};
const payload={id:activity,type:'issue.created',issue:{id:issue,app_id:app,title:'<!here> & <script>failure</script>',environment:'application',state:'open',assignee_team_id:team},details:{private:'must not be rendered'}};
function signed(body:Buffer,secret='test-secret') {
 const timestamp=String(Math.floor(Date.now()/1000));const id='delivery-one';
 return {'X-Faas-Delivery-Id':id,'X-Faas-Webhook-Timestamp':timestamp,'X-Faas-Webhook-Signature':'sha256='+createHmac('sha256',secret).update(`${timestamp}.${id}.`).update(body).digest('hex')};
}
test('verified Slack alerts retain team ownership without Slack mentions or private context',()=>{
 const body=Buffer.from(JSON.stringify({id:'delivery-one',app_id:app,rule_name:'issue.created',payload:{event:'issue.created',delivery_id:'delivery-one',attempt:0,data:payload}}));const out=issueSlackMessageFromWebhook('test-secret',signed(body),body,options);
 assert.equal(out.deliveryId,'delivery-one');assert.ok(out.message);
 assert.ok(out.message.text.includes('&lt;!here&gt;'));assert.ok(!out.message.text.includes('<!here>'));
 assert.equal(out.message.blocks[0]?.text.type,'plain_text');assert.ok(out.message.blocks[0]?.text.text.includes(`Team ${team}`));
 assert.equal(out.message.blocks[0]?.accessory.url,`https://gregale.dev/dashboard/apps/payments/issues?issue=${issue}`);
 assert.ok(!JSON.stringify(out.message).includes('must not be rendered'));
 assert.throws(()=>issueSlackMessageFromWebhook('wrong',signed(body),body,options),WebhookVerificationError);
 const invalid=Buffer.from('{broken');assert.throws(()=>issueSlackMessageFromWebhook('wrong',signed(invalid),invalid,options),WebhookVerificationError);
 assert.throws(()=>issueSlackMessageFromWebhook('test-secret',signed(invalid),invalid,options),/invalid issue webhook/);
});
test('Slack routing validates scope, configurable events and bounded plain text',()=>{
 assert.equal(formatIssueSlackMessage(payload,{...options,teamID:activity}),null);
 assert.equal(formatIssueSlackMessage(payload,{...options,appID:activity}),null);
 assert.equal(formatIssueSlackMessage({...payload,type:'issue.resolved'},options),null);
 assert.ok(formatIssueSlackMessage({...payload,type:'issue.resolved'},{...options,events:['issue.resolved']}));
 assert.equal(formatIssueSlackMessage({type:'deployment.live'},options),null);
 assert.throws(()=>formatIssueSlackMessage(payload,{...options,dashboardOrigin:'http://gregale.dev'}),/invalid issue Slack/);
 assert.throws(()=>formatIssueSlackMessage(payload,{...options,dashboardOrigin:'https://user:password@gregale.dev'}),/invalid issue Slack/);
 assert.throws(()=>formatIssueSlackMessage({...payload,issue:{...payload.issue,state:['open']}},options),/invalid issue webhook/);
 const big=formatIssueSlackMessage({...payload,issue:{...payload.issue,title:'😀'.repeat(10000)}},options);
 assert.ok(big);assert.ok(big.blocks[0]!.text.text.length<=2900);assert.ok(big.blocks[0]!.text.text.endsWith('…'));
});

test('JSON and CloudEvents retries bind stable transition content and delivery identity',()=>{
 const json=(attempt:number)=>Buffer.from(JSON.stringify({id:'delivery-one',app_id:app,rule_name:'issue.created',payload:{event:'issue.created',delivery_id:'delivery-one',attempt,data:payload}}));
 const first=json(0),retry=json(1);
 const a=issueSlackMessageFromWebhook('test-secret',signed(first),first,options);
 const b=issueSlackMessageFromWebhook('test-secret',signed(retry),retry,options);
 assert.equal(a.transitionSHA256,b.transitionSHA256);assert.deepEqual(a.message,b.message);
 const cloud=Buffer.from(JSON.stringify({specversion:'1.0',id:'delivery-one',type:'issue.created',appid:app,data:payload}));
 const c=issueSlackMessageFromWebhook('test-secret',signed(cloud),cloud,options);
 assert.equal(a.transitionSHA256,c.transitionSHA256);assert.deepEqual(a.message,c.message);
 const wrongID=Buffer.from(JSON.stringify({specversion:'1.0',id:'different',type:'issue.created',appid:app,data:payload}));
 assert.throws(()=>issueSlackMessageFromWebhook('test-secret',signed(wrongID),wrongID,options),/delivery ID/);
 const wrongApp=Buffer.from(JSON.stringify({specversion:'1.0',id:'delivery-one',type:'issue.created',appid:activity,data:payload}));
 assert.throws(()=>issueSlackMessageFromWebhook('test-secret',signed(wrongApp),wrongApp,options),/invalid issue webhook/);
});

test('frequency Slack alerts require explicit subscription and use a safe label',()=>{
 const frequency={...payload,type:'issue.frequency_threshold_reached'};
 assert.equal(formatIssueSlackMessage(frequency,options),null);
 const message=formatIssueSlackMessage(frequency,{...options,events:['issue.frequency_threshold_reached']});
 assert.ok(message?.text.includes('Event-frequency threshold reached'));
 assert.ok(!message?.text.includes('must not be rendered'));
});
