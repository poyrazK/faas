import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { evaluateFlag, flagBucket, GregaleFlags, type FlagsBundle } from '../src/flags.js';
const customer = '00000000-0000-0000-0000-000000000001';
const bundle = (): FlagsBundle => ({environment_id: customer, version: 1, groups: { internal: [customer] }, flags: [{key: 'export', enabled: true, default: false, seed: 'seed', rules: [{id: 'selected', group: 'internal', value: true}]}]});
test('cross-language allocation vectors', () => {
 const vectors = JSON.parse(readFileSync(new URL('../../../../pkg/flags/testdata/allocation.json',import.meta.url),'utf8')) as {seed:string;key:string;customer:string;bucket:number}[];
 for (const v of vectors) assert.equal(flagBucket(v.seed,v.key,v.customer),v.bucket);
});
test('targeting defaults and first-match explanation', () => {
 assert.equal(evaluateFlag(bundle(),'export',customer,false).rule_id,'selected');
 assert.equal(evaluateFlag(bundle(),'export',undefined,false).reason,'customer_missing');
 assert.equal(evaluateFlag(bundle(),'missing',customer,true).value,true);
});
test('request snapshots, explicit exposure, and restore freshness', async () => {
 let now = 0; let config = bundle(); let unavailable = false; let calls = 0;
 const fakeFetch: typeof fetch = async (input) => {
   calls++; if (unavailable) throw new Error('offline');
   return new Response(JSON.stringify(String(input).includes('127.0.0.1') ? {access_token:'token'} : config));
 };
 const flags = new GregaleFlags({apiURL:'https://api.example.com',identityEndpoint:'http://127.0.0.1:8082/token',fetch:fakeFetch,now:()=>now});
 await flags.refresh();
 await flags.runRequest({'X-Faas-Platform-Tenant-Id':customer},async () => {
  assert.equal(flags.boolean('export',false).value,true);
  config = {...bundle(),version:2,flags:[{...bundle().flags[0]!,enabled:false}]};await flags.refresh();
  assert.equal(flags.boolean('export',false).config_version,1);
  flags.used('export');assert.equal(flags.evidence()[0]?.used,true);
  assert.equal(JSON.parse(Buffer.from(flags.responseEvidence(),'base64url').toString())[0].rule_id,'selected');
 });
 await flags.runRequest({'X-Faas-Platform-Tenant-Id':customer},() => assert.equal(flags.boolean('export',true).value,false));
 now=120_000;config={...bundle(),version:3};
 await flags.runRequest({'X-Faas-Platform-Tenant-Id':customer},()=>assert.equal(flags.boolean('export',false).config_version,3));
 unavailable=true;now=240_000;
 await flags.runRequest({'X-Faas-Platform-Tenant-Id':customer},()=>assert.equal(flags.boolean('export',false).reason,'configuration_stale'));
 assert.ok(calls>=7);flags.close();
});
test('concurrent request contexts do not share customer decisions', async () => {
 const flags = new GregaleFlags({apiURL:'https://api.example.com',identityEndpoint:'http://127.0.0.1/token',fetch:async input=>new Response(JSON.stringify(String(input).includes('127.0.0.1')?{access_token:'token'}:bundle()))});
 await flags.refresh();
 await Promise.all([flags.runRequest({'X-Faas-Platform-Tenant-Id':customer},async()=>{await Promise.resolve();assert.equal(flags.boolean('export',false).value,true)}),flags.runRequest({},async()=>{await Promise.resolve();assert.equal(flags.boolean('export',true).value,false)})]);
 assert.throws(()=>flags.boolean('export',false));
});
test('evidence overflow keeps behavior checks usable with native Node headers', async () => {
 const flags = new GregaleFlags({apiURL:'https://api.example.com',identityEndpoint:'http://127.0.0.1/token',fetch:async input=>new Response(JSON.stringify(String(input).includes('127.0.0.1')?{access_token:'token'}:bundle()))});
 await flags.refresh();
 await flags.runRequest({'x-faas-platform-tenant-id':customer,'x-empty':undefined},()=>{
  for(let i=0;i<40;i++){assert.equal(flags.boolean(`flag-${i}`,true).value,true);flags.used(`flag-${i}`)}
  assert.equal(flags.evidence().length,32);
 });
});
test('cold startup during an API outage uses fallback behavior', async () => {
 const flags = new GregaleFlags({apiURL:'https://api.example.com',identityEndpoint:'http://127.0.0.1/token',fetch:async()=>{throw new Error('offline')}});
 await flags.start();
 await flags.runRequest({},()=>{const d=flags.boolean('export',false);assert.equal(d.value,false);assert.equal(d.reason,'configuration_stale')});
 flags.close();
});
