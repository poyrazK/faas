import test from 'node:test';
import assert from 'node:assert/strict';
import { once, EventEmitter } from 'node:events';
import { observeProtocolRequest, observeServerTransport } from '../protocol-telemetry.js';
import { setTimeout as delay } from 'node:timers/promises';
import { createApp } from '../app.js';

const rid = 'a1234567-1234-4234-8234-123456789abc';
const trace = '123456789abcdef0123456789abcdef0';
const envelope = { 'io.modelcontextprotocol/protocolVersion': '2026-07-28', 'io.modelcontextprotocol/clientInfo': { name: 'telemetry-test', version: '1' }, 'io.modelcontextprotocol/clientCapabilities': {} };

test('protocol wire outcomes, bounded labels and safe correlation', async t => {
  const logs = [];
  const { app } = createApp({ version: 1, endpoint: '/mcp', transport: 'streamable-http', mode: 'stateless', legacy: true, allowed_origins: [], auth: { mode: 'open' } }, { log: value => logs.push(JSON.parse(value)) });
  const server = app.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise(resolve => { server.close(resolve); server.closeAllConnections(); }));
  const url = `http://127.0.0.1:${server.address().port}/mcp`;
  async function call(method, params, outcome, tool) {
    const body = { jsonrpc: '2.0', id: 'private-rpc-id', method, params: { ...params, _meta: envelope } };
    const response = await fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream', 'MCP-Protocol-Version': '2026-07-28', 'MCP-Method': method, ...(params.name ? { 'MCP-Name': params.name } : {}), ...(params.uri ? { 'MCP-Name': params.uri } : {}), 'X-Faas-Request-Id': rid, traceparent: `00-${trace}-123456789abcdef0-01`, Authorization: 'Bearer do-not-log-this' }, body: JSON.stringify(body) });
    await response.text();
    for (let i=0; i<50 && !logs.some(x=>x.event==='mcp_protocol_request'); i++) await delay(10);
    const events = logs.filter(x=>x.event==='mcp_protocol_request');assert.equal(events.length,1,JSON.stringify(logs));
    const event=events[0];assert.equal(event.method,method);assert.equal(event.tool,tool);assert.equal(event.outcome,outcome);assert.equal(event.request_id,rid);assert.equal(event.trace_id,trace);assert.ok(event.execution_duration_ms>=0);
    const serialized=JSON.stringify(event);for(const secret of ['private-rpc-id','do-not-log-this','sensitive-payload','missing://private-resource'])assert.ok(!serialized.includes(secret));
    logs.length=0;
  }
  await call('tools/call',{name:'greet',arguments:{name:'sensitive-payload'}},'success','greet');
  await call('tools/call',{name:'add',arguments:{a:'sensitive-payload',b:2}},'tool_error','add');
  await call('resources/read',{uri:'missing://private-resource'},'rpc_error','none');
});

test('disconnect is counted once, without inventing a successful result', async t => {
  const logs=[];const {app}=createApp({version:1,endpoint:'/mcp',transport:'streamable-http',mode:'stateless',legacy:true,allowed_origins:[],auth:{mode:'open'}},{log:value=>logs.push(JSON.parse(value))});
  const server=app.listen(0,'127.0.0.1');await once(server,'listening');t.after(()=>new Promise(resolve=>{server.close(resolve);server.closeAllConnections();}));
  const controller=new AbortController();
  const response=await fetch(`http://127.0.0.1:${server.address().port}/mcp`,{method:'POST',signal:controller.signal,headers:{'Content-Type':'application/json',Accept:'text/event-stream','MCP-Protocol-Version':'2026-07-28','MCP-Method':'tools/call','MCP-Name':'stream_demo'},body:JSON.stringify({jsonrpc:'2.0',id:1,method:'tools/call',params:{name:'stream_demo',arguments:{},_meta:{...envelope,progressToken:'private-progress'}}})});
  const reader=response.body.getReader();await reader.read();controller.abort();
  for(let i=0;i<100&&!logs.some(x=>x.event==='mcp_protocol_request');i++)await delay(10);
  const events=logs.filter(x=>x.event==='mcp_protocol_request');assert.equal(events.length,1);assert.equal(events[0].outcome,'cancelled');assert.equal(events[0].stream_outcome,'cancelled');assert.ok(events[0].stream_duration_ms>=0);
});

test('telemetry sink failure does not fail a protocol response', async t => {
  const {app}=createApp({version:1,endpoint:'/mcp',transport:'streamable-http',mode:'stateless',legacy:true,allowed_origins:[],auth:{mode:'open'}},{log:()=>{throw new Error('private-sink-error');}});
  const server=app.listen(0,'127.0.0.1');await once(server,'listening');t.after(()=>new Promise(resolve=>{server.close(resolve);server.closeAllConnections();}));
  const response=await fetch(`http://127.0.0.1:${server.address().port}/mcp`,{method:'POST',headers:{'Content-Type':'application/json','MCP-Protocol-Version':'2026-07-28','MCP-Method':'tools/list',Accept:'application/json, text/event-stream'},body:JSON.stringify({jsonrpc:'2.0',id:1,method:'tools/list',params:{_meta:envelope}})});
  assert.equal(response.status,200);assert.match(await response.text(),/"result"/);
});


test('typed transport timeout and invalid correlation', () => {
  const events=[];
  const res=new EventEmitter();res.getHeader=()=>undefined;res.writeHead=()=>res;
  const req={body:{jsonrpc:'2.0',id:1,method:'private-method-token',params:{name:'private-tool-token'}},headers:{'x-faas-request-id':`${rid}, ${rid}`,traceparent:`00-${'0'.repeat(32)}-123456789abcdef0-01`}};
  observeProtocolRequest(req,res,value=>events.push(JSON.parse(value)));
  res.emit('error',Object.assign(new Error('private-error-text'),{name:'TimeoutError'}));res.emit('close');
  assert.equal(events.length,1);assert.equal(events[0].method,'other');assert.equal(events[0].outcome,'timeout');assert.ok(!Object.hasOwn(events[0],'execution_duration_ms'));assert.ok(!Object.hasOwn(events[0],'request_id'));assert.ok(!Object.hasOwn(events[0],'trace_id'));assert.ok(!JSON.stringify(events[0]).includes('private-'));
});


test('failed SDK send preserves RPC error and records delivery failure', async () => {
  const events=[];const res=new EventEmitter();res.getHeader=()=>undefined;res.writeHead=()=>res;
  const observation=observeProtocolRequest({body:{id:1,method:'resources/read'},headers:{}},res,value=>events.push(JSON.parse(value)));
  const server={connect:async()=>{}};const transport={send:async()=>{throw Object.assign(new Error('private-send-error'),{code:'EPIPE'});}};
  observeServerTransport(server,observation);await server.connect(transport);
  await assert.rejects(transport.send({jsonrpc:'2.0',id:1,error:{code:-32603,message:'private-protocol-error'}}));
  res.emit('finish');assert.equal(events.length,1);assert.equal(events[0].outcome,'rpc_error');assert.equal(events[0].stream_outcome,'cancelled');assert.ok(!JSON.stringify(events[0]).includes('private-'));
});

test('heartbeat is independent of requests and stops cleanly', async () => {
  const { startProtocolHeartbeat } = await import('../protocol-telemetry.js');
  const events = [];
  const stop = startProtocolHeartbeat(line => events.push(JSON.parse(line)));
  stop(); stop();
  assert.equal(events.length, 1);
  assert.equal(events[0].event, 'mcp_telemetry_heartbeat');
  assert.equal(events[0].version, 1);
  assert.deepEqual(Object.keys(events[0]).sort(), ['event', 'loss_alert', 'version']);
  const stopFailedSink = startProtocolHeartbeat(() => { throw new Error('sink failed'); });
  stopFailedSink();
});
