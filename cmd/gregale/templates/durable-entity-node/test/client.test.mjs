import test from 'node:test';
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {FaaSClient, DurableEntityResultDecodeError} from '@gregale/sdk-node';
import {counter} from '../client.mjs';

test('generated client validates inputs, binds scope, preserves request ID and exposes observation', async t => {
  const bodies = [];
  const server = createServer(async (req, res) => {
    res.setHeader('Content-Type', 'application/json');
    if (req.method === 'GET') {
      assert.equal(new URL(req.url, 'http://localhost').searchParams.get('key'), 'customer:1');
      res.end(JSON.stringify({entity: {account_id:'a',app_id:'b',environment_id:'e',namespace:'counters',key:'customer:1'},version:1,state_committed:true,recovery_revision:'r',alarm:{attempts:0,exhausted:false},outbox:{pending:0,attempts:0,exhausted:false}})); return;
    }
    const chunks = []; for await (const chunk of req) chunks.push(chunk);
    const body = JSON.parse(Buffer.concat(chunks)); bodies.push(body);
    res.end(JSON.stringify({version:1,replayed:bodies.length>1,value:bodies.length===3?{count:'bad'}:{count:1}}));
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => {server.closeAllConnections();server.close();});
  new FaaSClient(`http://127.0.0.1:${server.address().port}`, {token:'test',retry:{maxAttempts:1,backoffMs:0}});
  const handle = counter({slug:'app',key:'customer:1',environment:'staging'});
  await assert.rejects(handle.invoke('one',{delta:'bad'}));
  await assert.rejects(handle.invoke('',{delta:1})); assert.equal(bodies.length,0);
  const payload={delta:1};const pending=handle.invoke('one',payload);payload.delta=99;
  assert.equal((await pending).replayed,false);
  assert.equal((await handle.invoke('one',{delta:1})).replayed,true);
  assert.deepEqual(bodies[0],bodies[1]);assert.equal(bodies[0].namespace,'counters');assert.equal(bodies[0].request_id,'one');
  await assert.rejects(handle.invoke('one',{delta:1}),error=>error instanceof DurableEntityResultDecodeError&&error.version===1&&error.replayed);
  assert.equal((await handle.observe()).outbox_status,'empty');
});
