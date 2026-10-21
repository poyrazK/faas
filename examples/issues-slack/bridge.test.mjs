import test from 'node:test';
import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createHash, createHmac, randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { Pool } from 'pg';
import { createIssueSlackServer } from './server.mjs';
import { PostgresReceipts, ReceiptConflict } from './receipts.mjs';

const app='11111111-1111-4111-8111-111111111111';
const issue='22222222-2222-4222-8222-222222222222';
const team='33333333-3333-4333-8333-333333333333';
const activity='44444444-4444-4444-8444-444444444444';
const options={appID:app,appSlug:'payments',dashboardOrigin:'https://gregale.dev',teamID:team};
const secret='test-secret';
const payload={id:activity,type:'issue.created',issue:{id:issue,app_id:app,title:'Payment failure',environment:'application',state:'open',assignee_team_id:team}};
function wire(transition,id,attempt=0) {
 return JSON.stringify({id,app_id:app,rule:'app.webhook',rule_name:transition.type,payload:{event:transition.type,delivery_id:id,attempt,data:transition}});
}
function request(transition,id,signingSecret=secret,attempt=0) {
 const body=wire(transition,id,attempt);const timestamp=String(Math.floor(Date.now()/1000));
 return {method:'POST',body,headers:{'content-type':'application/json','x-faas-delivery-id':id,'x-faas-webhook-timestamp':timestamp,
  'x-faas-webhook-signature':'sha256='+createHmac('sha256',signingSecret).update(`${timestamp}.${id}.`).update(body).digest('hex')}};
}
async function listen(t,server) {
 server.listen(0,'127.0.0.1');await once(server,'listening');
 t.after(()=>new Promise((resolve,reject)=>server.close(error=>error?reject(error):resolve())));
 return `http://127.0.0.1:${server.address().port}/webhooks/gregale`;
}
test('bridge verifies before claiming, skips other teams, and never echoes secrets',async(t)=>{
 let claims=0,sends=0;const receipts={claim:async()=>{claims++;return {status:'claimed',token:randomUUID()}},complete:async()=>{},release:async()=>{}};
 const server=createIssueSlackServer({secret,slackURL:'https://hooks.slack.com/services/T/B/secret',options,receipts,fetch:async()=>{sends++;return new Response('ok')}});
 const url=await listen(t,server);const body=payload;
 assert.equal((await fetch(url,request(body,'bad','wrong'))).status,401);
 assert.equal(claims,0);assert.equal(sends,0);
 const filtered={...payload,issue:{...payload.issue,assignee_team_id:activity}};
 assert.equal((await fetch(url,request(filtered,'filtered'))).status,204);assert.equal(claims,0);
 assert.equal((await fetch(url,request(body,'good'))).status,204);assert.equal(claims,1);assert.equal(sends,1);
 assert.throws(()=>createIssueSlackServer({secret,slackURL:'https://example.com/private-secret',options,receipts}),/invalid Slack webhook configuration/);
 assert.throws(()=>createIssueSlackServer({secret,slackURL:'https://hooks.slack.com/services/T/B/secret',options:{...options,appID:'bad'},receipts}),/invalid issue Slack options/);
});
test('Postgres receipts survive restart, fence concurrent claims and recover retries',{skip:!process.env.ISSUE_SLACK_TEST_DATABASE_URL,timeout:15000},async(t)=>{
 const schema='issue_slack_'+randomUUID().replaceAll('-','');
 const admin=new Pool({connectionString:process.env.ISSUE_SLACK_TEST_DATABASE_URL});
 await admin.query(`CREATE SCHEMA ${schema}`);
 const pool=new Pool({connectionString:process.env.ISSUE_SLACK_TEST_DATABASE_URL,options:`-c search_path=${schema}`});
 t.after(async()=>{await pool.end();await admin.query(`DROP SCHEMA ${schema} CASCADE`);await admin.end()});
 await pool.query(await readFile(new URL('schema.sql',import.meta.url),'utf8'));
 const receipts=new PostgresReceipts(pool),hash=createHash('sha256').update('body').digest('hex');
 const results=await Promise.all([receipts.claim('concurrent',hash),receipts.claim('concurrent',hash)]);
 assert.deepEqual(results.map(x=>x.status).sort(),['busy','claimed']);
 const first=results.find(x=>x.status==='claimed');
 await pool.query("UPDATE gregale_issue_slack_receipts SET lease_until=now()-interval '1 second' WHERE delivery_id='concurrent'");
 const recovered=await receipts.claim('concurrent',hash);assert.equal(recovered.status,'claimed');
 await assert.rejects(()=>receipts.complete('concurrent',first.token),/lease changed/);
 await receipts.complete('concurrent',recovered.token);
 assert.equal((await new PostgresReceipts(pool).claim('concurrent',hash)).status,'sent');
 await assert.rejects(()=>receipts.claim('concurrent','a'.repeat(64)),ReceiptConflict);
 let attempts=0;
 const receiver=createIssueSlackServer({secret,slackURL:'https://hooks.slack.com/services/T/B/secret',options,receipts,
  fetch:async(_url,init)=>{assert.equal(init.redirect,'error');assert.ok(JSON.parse(init.body).text.includes('Payment failure'));attempts++;return new Response('ok',{status:attempts===1?429:200})}});
 const url=await listen(t,receiver),body=payload;
 const failure=await fetch(url,request(body,'retry'));assert.equal(failure.status,503);assert.ok(!(await failure.text()).includes('secret'));
 assert.equal((await fetch(url,request(body,'retry',secret,1))).status,204);
 assert.equal((await fetch(url,request(body,'retry',secret,2))).status,204);assert.equal(attempts,2);
 const restarted=createIssueSlackServer({secret,slackURL:'https://hooks.slack.com/services/T/B/secret',options,receipts:new PostgresReceipts(pool),fetch:async()=>{throw new Error('completed retry must not send')}});
 const restartedURL=await listen(t,restarted);assert.equal((await fetch(restartedURL,request(body,'retry'))).status,204);
});
