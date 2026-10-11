import test from 'node:test';
import assert from 'node:assert/strict';
import { OpenAPI, StorageService } from '../src/generated/index.js';

test('version protection keeps the caller retry ID and exact owned selector', async () => {
  const oldFetch = globalThis.fetch;
  const oldBase = OpenAPI.BASE;
  const oldToken = OpenAPI.TOKEN;
  const id = '00000000-0000-4000-8000-000000000001';
  const versionId = '00000000-0000-4000-8000-000000000002';
  const key = '目录/+ %';
  let calls = 0;
  OpenAPI.BASE = 'https://api.example.test';
  OpenAPI.TOKEN = 'token';
  globalThis.fetch = async (input, init) => {
    calls++;
    const url = new URL(String(input));
    assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer token');
    if (!url.pathname.includes('protection-operations')) {
      assert.equal(url.searchParams.get('key'), key);
      assert.equal(url.searchParams.get('version_id'), versionId);
    }
    if (init?.method === 'PUT') {
      const body = JSON.parse(String(init.body));
      assert.equal(body.id, id);
      if (url.pathname.endsWith('legal-hold')) assert.deepEqual(body.legal_hold, {status:'OFF'});
      else assert.deepEqual(body.retention, {});
      return Response.json({id,state:'waiting'}, {status:202});
    }
    if (url.pathname.includes('protection-operations')) return Response.json({id,state:'ready'});
    if (url.pathname.endsWith('retention')) return Response.json({version_id:versionId,retention:{}});
    return Response.json({version_id:versionId,legal_hold:{status:'ON'}});
  };
  try {
    const args = {slug:'demo',bucket:'bucket',key,versionId};
    const read = await StorageService.getObjectVersionLegalHold(args);
    assert.ok('legal_hold' in read && read.legal_hold.status === 'ON');
    const accepted = await StorageService.putObjectVersionLegalHold({...args,requestBody:{id,legal_hold:{status:'OFF'}}});
    assert.ok('state' in accepted && accepted.state === 'waiting');
    await StorageService.getObjectVersionRetention(args);
    await StorageService.putObjectVersionRetention({...args,requestBody:{id,retention:{}}});
    const done = await StorageService.getObjectVersionProtection({slug:'demo',bucket:'bucket',operation:id});
    assert.ok('state' in done && done.state === 'ready');
    assert.equal(calls, 5);
  } finally {
    globalThis.fetch = oldFetch;
    OpenAPI.BASE = oldBase;
    OpenAPI.TOKEN = oldToken;
  }
});

test('event holds preserve nested duration and conditional release intent', async () => {
  const oldFetch = globalThis.fetch;
  const policies = [
    {mode:'COMPLIANCE' as const,event_hold:'ON' as const,event_hold_duration:{days:30}},
    {mode:'GOVERNANCE' as const,event_hold:'ON' as const,event_hold_duration:{years:1}},
    {mode:'COMPLIANCE' as const,event_hold:'OFF' as const},
  ];
  let index = 0;
  globalThis.fetch = async (input, init) => {
    const url = new URL(String(input));
    assert.equal(url.searchParams.get('version_id'), 'null');
    assert.equal(url.searchParams.get('key'), '目录/+ %');
    assert.deepEqual(JSON.parse(String(init?.body)), {id:'00000000-0000-4000-8000-000000000001',retention:policies[index++]});
    return Response.json({id:'00000000-0000-4000-8000-000000000001',state:'waiting'}, {status:202});
  };
  try {
    for (const retention of policies) {
      await StorageService.putObjectVersionRetention({slug:'demo',bucket:'bucket',key:'目录/+ %',versionId:'null',requestBody:{id:'00000000-0000-4000-8000-000000000001',retention}});
    }
    assert.equal(index, policies.length);
  } finally {globalThis.fetch = oldFetch;}
});
