import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { FaaSClient, McpService, type SetMCPReadinessFallbackRequest, type MCPReadinessFallbackPolicy, type SetMCPReadinessPagerDutyAcknowledgementsRequest, type MCPReadinessPagerDutyAcknowledgementPolicy, type MCPReadinessPagerDutyAcknowledgementEvent } from '../src/index.js';

test('native PagerDuty fallback and activated receiver acknowledgement contracts', { timeout: 10000 }, async (t) => {
 const rule = '00000000-0000-0000-0000-000000000001';
 const base = `/v1/apps/demo/alerts/${rule}`;
 const request: SetMCPReadinessFallbackRequest = {expected_version:0,enabled:true,provider:'pagerduty',region:'eu',integration_key:'a'.repeat(32),after_failures:2};
 const policy: MCPReadinessFallbackPolicy = {version:1,enabled:true,provider:'pagerduty',region:'eu',integration_key_masked:'***',after_failures:2};
 const binding: SetMCPReadinessPagerDutyAcknowledgementsRequest = {expected_version:0,enabled:true,destination:'fallback',expected_fallback_version:1,service_id:'PSVC001',signing_secret:'s'.repeat(32)};
 const bound: MCPReadinessPagerDutyAcknowledgementPolicy = {version:1,enabled:true,destination:'fallback',fallback_version:1,region:'eu',service_id:'PSVC001',webhook_path:`/v1/webhooks/mcp-readiness/pagerduty/${rule}`};
 const event: MCPReadinessPagerDutyAcknowledgementEvent = {id:rule,event_id:'fallback-event',incident_id:rule,provider_incident_id:'PINC001',occurred_at:'2026-10-17T00:00:00Z',received_at:'2026-10-17T00:00:01Z',policy_version:1,outcome:'acknowledged',attention_version:1,region:'eu',service_id:'PSVC001',destination:'fallback'};
 let calls = 0;
 const server = createServer(async (req,res) => {
  calls++;res.setHeader('Content-Type','application/json');
  if (calls===4) {assert.equal(req.url,`${base}/mcp-readiness-pagerduty-acknowledgements/events?limit=1`);res.end(JSON.stringify({events:[event]}));return;}
  let raw='';for await(const chunk of req)raw+=chunk;
  assert.equal(req.method,'PUT');
  if(calls===3){assert.equal(req.url,`${base}/mcp-readiness-pagerduty-acknowledgements`);assert.deepEqual(JSON.parse(raw),binding);res.end(JSON.stringify(bound));return;}
  assert.equal(req.url,`${base}/mcp-readiness-fallback`);
  assert.deepEqual(JSON.parse(raw),calls===1?request:{expected_version:1,enabled:false});res.end(JSON.stringify(policy));
 });
 server.listen(0,'127.0.0.1');await once(server,'listening');t.after(()=>{server.closeAllConnections();server.close();});
 const address=server.address();assert.ok(address && typeof address!=='string');new FaaSClient(`http://127.0.0.1:${address.port}`,{token:'fixture',retry:{maxAttempts:1,backoffMs:0}});
 assert.deepEqual(await McpService.setMcpReadinessFallback({slug:'demo',id:rule,requestBody:request}),policy);
 await McpService.setMcpReadinessFallback({slug:'demo',id:rule,requestBody:{expected_version:1,enabled:false}});
 assert.deepEqual(await McpService.setMcpReadinessPagerDutyAcknowledgements({slug:'demo',id:rule,requestBody:binding}),bound);
 assert.deepEqual(await McpService.listMcpReadinessPagerDutyAcknowledgementEvents({slug:'demo',id:rule,limit:1}),{events:[event]});assert.equal(calls,4);
});
