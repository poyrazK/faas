import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { evaluateFlag, evaluateVariant, flagBucket, flagVariantBucket, flagSubjectBucket, flagSubjectVariantBucket, GregaleFlags, GREGALE_FLAG_PROPAGATION_HEADER, type FlagsBundle } from '../src/flags.js';
import { createGregaleFetch } from '../src/release-context.js';
const customer = '00000000-0000-0000-0000-000000000001';
const bundle = (): FlagsBundle => ({environment_id: customer, version: 1, groups: { internal: [customer] }, flags: [{key: 'export', enabled: true, default: false, seed: 'seed', rules: [{id: 'selected', group: 'internal', value: true}]}]});
test('cross-language allocation vectors', () => {
 const vectors = JSON.parse(readFileSync(new URL('../../../../pkg/flags/testdata/allocation.json',import.meta.url),'utf8')) as {seed:string;key:string;customer:string;bucket:number}[];
 for (const v of vectors) assert.equal(flagBucket(v.seed,v.key,v.customer),v.bucket);
});
test('cross-language variant allocation vectors', () => {
 const vectors = JSON.parse(readFileSync(new URL('../../../../pkg/flags/testdata/variant_allocation.json',import.meta.url),'utf8')) as {seed:string;key:string;customer:string;bucket:number}[];
 for (const v of vectors) assert.equal(flagVariantBucket(v.seed,v.key,v.customer),v.bucket);
});
test('cross-language subject allocation vectors', () => {
 const vectors = JSON.parse(readFileSync(new URL('../../../../pkg/flags/testdata/subject_allocation.json',import.meta.url),'utf8')) as {seed:string;key:string;customer:string;subject:string;bucket:number;variant_bucket:number}[];
 for (const v of vectors) { assert.equal(flagSubjectBucket(v.seed,v.key,v.customer,v.subject),v.bucket); assert.equal(flagSubjectVariantBucket(v.seed,v.key,v.customer,v.subject),v.variant_bucket); }
});
const variants = (): FlagsBundle => ({environment_id:customer,version:5,groups:{},flags:[{key:'checkout',type:'variant',enabled:true,default:'control',seed:'stable',variants:[{key:'control',weight:5000},{key:'treatment',weight:5000}],rules:[{id:'eligible',rollout:10000}]}]});
test('weighted variants are sticky, explainable, and independently rollout-gated', () => {
 const first = evaluateVariant(variants(),'checkout',customer,'fallback');
 assert.equal(first.type,'variant');assert.equal(first.value,evaluateVariant(variants(),'checkout',customer,'fallback').value);
 assert.equal(first.bucket,flagVariantBucket('stable','checkout',customer));assert.equal(first.rule_id,'eligible');
 const mismatched = evaluateFlag(variants(),'checkout',customer,false);assert.equal(mismatched.reason,'type_mismatch');assert.equal(mismatched.value,false);
 const targeted = variants();targeted.flags[0] = {key:'checkout',type:'variant',enabled:true,default:'control',seed:'stable',variants:[{key:'control',weight:5000},{key:'treatment',weight:5000}],rules:[{id:'selected',customers:[customer],value:'treatment'}]};
 assert.equal(evaluateVariant(targeted,'checkout',customer,'fallback').value,'treatment');
});
test('targeting defaults and first-match explanation', () => {
 assert.equal(evaluateFlag(bundle(),'export',customer,false).rule_id,'selected');
 assert.equal(evaluateFlag(bundle(),'export',undefined,false).reason,'customer_missing');
 assert.equal(evaluateFlag(bundle(),'missing',customer,true).value,true);
});
test('subject targeting uses tenant-scoped sticky allocation and fails closed without an actor', () => {
 const targeted: FlagsBundle = {environment_id:customer,version:2,groups:{},flags:[{key:'new-export',enabled:true,default:false,seed:'stable',rules:[{id:'pilot-users',customers:[customer],rollout:6000,rollout_unit:'subject',value:true}]}]};
 const first=evaluateFlag(targeted,'new-export',customer,false,'user-17');
 const second=evaluateFlag(targeted,'new-export',customer,false,'user-18');
 assert.equal(first.value,false);assert.equal(first.reason,'default');assert.equal(flagSubjectBucket('stable','new-export',customer,'user-17'),6443);
 assert.equal(second.value,true);assert.equal(second.bucket,5279);
 assert.equal(evaluateFlag(targeted,'new-export',customer,false).reason,'subject_missing');
 assert.notEqual(flagSubjectBucket('stable','new-export',customer,'user-17'),flagSubjectBucket('stable','new-export','00000000-0000-0000-0000-000000000004','user-17'));
 const variants: FlagsBundle = {environment_id:customer,version:2,groups:{},flags:[{key:'checkout',type:'variant',enabled:true,default:'control',seed:'stable',variants:[{key:'control',weight:5000},{key:'treatment',weight:5000}],rules:[{id:'pilot-users',customers:[customer],subjects:['user-17'] }]}]};
 const userVariant=evaluateVariant(variants,'checkout',customer,'control','user-17');
 const userBucket=flagSubjectVariantBucket('stable','checkout',customer,'user-17');
 assert.equal(userVariant.bucket,userBucket);
 assert.equal(userVariant.value,userBucket<5000?'control':'treatment');
 assert.equal(evaluateVariant(variants,'checkout',customer,'control','user-18').reason,'default');
});
test('request snapshots, explicit exposure, and restore freshness', async () => {
 let now = 0; let config = bundle(); let unavailable = false; let calls = 0;
 const fakeFetch: typeof fetch = async (input, init) => {
   calls++; if (unavailable) throw new Error('offline');
   if (String(input).includes('127.0.0.1')) return new Response(JSON.stringify({access_token:'token'}));
   assert.equal(new Headers(init?.headers).get('X-Faas-Flags-Capabilities'),'subject-targeting-v1');
   return new Response(JSON.stringify(config));
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
test('request-scoped variant decisions are included in application evidence', async () => {
 const flags = new GregaleFlags({apiURL:'https://api.example.com',identityEndpoint:'http://127.0.0.1/token',fetch:async input=>new Response(JSON.stringify(String(input).includes('127.0.0.1')?{access_token:'token'}:variants()))});
 await flags.refresh();
 await flags.runRequest({'X-Faas-Platform-Tenant-Id':customer},()=>{
  const decision = flags.variant('checkout','fallback');
  assert.equal(decision.type,'variant');assert.ok(['control','treatment'].includes(decision.value));
  flags.used('checkout');
  assert.deepEqual(flags.evidence()[0],{...decision,used:true});
 });
 flags.close();
});
test('subject scope is established after authentication and evidence omits the subject ID', async () => {
 const config: FlagsBundle = {environment_id:customer,version:2,groups:{},flags:[{key:'new-export',enabled:true,default:false,seed:'stable',rules:[{id:'pilot-users',customers:[customer],subjects:['user-17'],value:true}]}]};
 const flags=new GregaleFlags({apiURL:'https://api.example.com',identityEndpoint:'http://127.0.0.1/token',fetch:async input=>new Response(JSON.stringify(String(input).includes('127.0.0.1')?{access_token:'token'}:config))});
 await flags.refresh();
 await flags.runRequest({'X-Faas-Platform-Tenant-Id':customer},async()=>{
  assert.throws(()=>flags.withSubject('not allowed',()=>Promise.resolve()),/Invalid opaque subject ID/);
  await flags.withSubject('user-17',()=>{
   assert.equal(flags.boolean('new-export',false).value,true);
   flags.used('new-export');
  });
  const raw=Buffer.from(flags.responseEvidence(),'base64url').toString();
  assert.match(raw,/"rule_id":"pilot-users"/);assert.doesNotMatch(raw,/user-17/);
 });
 flags.close();
});
test('runtime bundle validation rejects an empty subject target list', async () => {
 const config: FlagsBundle = {environment_id:customer,version:2,groups:{},flags:[{key:'new-export',enabled:true,default:false,seed:'stable',rules:[{id:'pilot-users',customers:[customer],subjects:[],value:true}]}]};
 const flags=new GregaleFlags({apiURL:'https://api.example.com',identityEndpoint:'http://127.0.0.1/token',fetch:async input=>new Response(JSON.stringify(String(input).includes('127.0.0.1')?{access_token:'token'}:config))});
 await assert.rejects(flags.refresh(),/Invalid Flags rule/);
 flags.close();
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

test('managed service fetch forwards only used decisions and strips context from external origins', async () => {
 const priorAppID = process.env.FAAS_APP_ID;
 process.env.FAAS_APP_ID = '00000000-0000-0000-0000-000000000002';
 const flags = new GregaleFlags({apiURL:'https://api.example.com',identityEndpoint:'http://127.0.0.1/token',fetch:async input=>new Response(JSON.stringify(String(input).includes('127.0.0.1')?{access_token:'token'}:bundle()))});
 const calls: Array<{url:string;headers:Headers}> = [];
 const fetcher = createGregaleFetch(async (input, init) => {
  calls.push({url:input instanceof Request?input.url:String(input),headers:new Headers(init?.headers)});
  return new Response(null,{status:204});
 },{flags});
 try {
  await flags.refresh();
  await flags.runRequest({'X-Faas-Platform-Tenant-Id':customer},async()=>{
   flags.boolean('export',false);flags.used('export');
   flags.boolean('not-used',true);
   await fetcher('http://billing.svc.gregale/charge',{headers:{[GREGALE_FLAG_PROPAGATION_HEADER]:'forged'}});
   await fetcher('https://payments.example.test/charge',{headers:{[GREGALE_FLAG_PROPAGATION_HEADER]:'forged'}});
  });
  const propagated = calls[0]?.headers.get(GREGALE_FLAG_PROPAGATION_HEADER);
  assert.ok(propagated);
  const context = JSON.parse(Buffer.from(propagated,'base64url').toString('utf8')) as {customer_id:string;decisions:Array<{flag:string;value:boolean;config_version:number;origin:{app_id:string;environment_id:string}}>;version:number};
  assert.equal(context.version,1);assert.equal(context.customer_id,customer);
  assert.deepEqual(context.decisions.map(decision=>decision.flag),['export']);
  assert.equal(context.decisions[0]?.value,true);assert.equal(context.decisions[0]?.config_version,1);
  assert.equal(context.decisions[0]?.origin.app_id,process.env.FAAS_APP_ID);
  assert.equal(context.decisions[0]?.origin.environment_id,customer);
  assert.equal(calls[1]?.headers.has(GREGALE_FLAG_PROPAGATION_HEADER),false);
 } finally {
  flags.close();
  if(priorAppID===undefined) delete process.env.FAAS_APP_ID; else process.env.FAAS_APP_ID=priorAppID;
 }
});

test('propagated decisions override downstream config and retain their origin', async () => {
 const originApp='00000000-0000-0000-0000-000000000002';
 const originEnvironment='00000000-0000-0000-0000-000000000003';
 const downstream: FlagsBundle = {...variants(),version:99,flags:[
  {key:'export',enabled:false,default:false,seed:'downstream',rules:[]},
  {key:'checkout',type:'variant',enabled:true,default:'control',seed:'downstream',variants:[{key:'control',weight:5000},{key:'treatment',weight:5000}],rules:[{id:'local',rollout:10000}]},
 ]};
 const context=Buffer.from(JSON.stringify({version:1,customer_id:customer,decisions:[
  {flag:'export',value:true,config_version:7,rule_id:'selected',reason:'rule_match',source:'configuration',origin:{app_id:originApp,environment_id:originEnvironment}},
  {flag:'checkout',type:'variant',value:'treatment',config_version:8,rule_id:'variant-rule',reason:'rule_match',source:'configuration',origin:{app_id:originApp,environment_id:originEnvironment}},
 ]})).toString('base64url');
 const flags=new GregaleFlags({apiURL:'https://api.example.com',identityEndpoint:'http://127.0.0.1/token',fetch:async input=>new Response(JSON.stringify(String(input).includes('127.0.0.1')?{access_token:'token'}:downstream))});
 await flags.refresh();
 await flags.runRequest({'X-Faas-Platform-Tenant-Id':customer,[GREGALE_FLAG_PROPAGATION_HEADER]:context},()=>{
  const boolean=flags.boolean('export',false);flags.used('export');
  assert.equal(boolean.value,true);assert.equal(boolean.source,'inherited');assert.equal(boolean.config_version,7);
  assert.deepEqual(boolean.inherited_from,{app_id:originApp,environment_id:originEnvironment});
  const variant=flags.variant('checkout','control');flags.used('checkout');
  assert.equal(variant.value,'treatment');assert.equal(variant.source,'inherited');assert.equal(variant.config_version,8);
  assert.deepEqual(variant.inherited_from,{app_id:originApp,environment_id:originEnvironment});
  assert.equal(flags.evidence().length,2);
 });
 flags.close();
});

test('a customer mismatch disables inherited flag context', async () => {
 const originApp='00000000-0000-0000-0000-000000000002';
 const context=Buffer.from(JSON.stringify({version:1,customer_id:customer,decisions:[
  {flag:'export',value:true,config_version:7,reason:'default',source:'configuration',origin:{app_id:originApp,environment_id:customer}},
 ]})).toString('base64url');
 const flags=new GregaleFlags({apiURL:'https://api.example.com',identityEndpoint:'http://127.0.0.1/token',fetch:async input=>new Response(JSON.stringify(String(input).includes('127.0.0.1')?{access_token:'token'}:bundle()))});
 await flags.refresh();
 await flags.runRequest({'X-Faas-Platform-Tenant-Id':'00000000-0000-0000-0000-000000000004',[GREGALE_FLAG_PROPAGATION_HEADER]:context},()=>{
  const decision=flags.boolean('export',false);
  assert.equal(decision.value,false);assert.equal(decision.source,'configuration');
 });
 flags.close();
});
