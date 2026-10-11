import assert from 'node:assert/strict';
import {inspect} from 'node:util';
import {test} from 'node:test';
import {ObjectStorageBinding} from '../src/object-storage-runtime.js';

const values = () => ({ASSETS_ENDPOINT:'https://s3.gregale.test',ASSETS_REGION:'auto',ASSETS_BUCKET:'assets',ASSETS_ACCESS_KEY_ID:'private-access',ASSETS_SECRET_ACCESS_KEY:'private-secret',ASSETS_ADDRESSING_STYLE:'path'});

test('atomic rotation, SDK option spreading, safe errors and revocation', async () => {
  const b = new ObjectStorageBinding('ASSETS', config => {
    assert.equal(config.forcePathStyle,true);
    assert.equal(config.requestChecksumCalculation,'WHEN_REQUIRED');
    assert.equal(config.responseChecksumValidation,'WHEN_REQUIRED');
    assert.equal(config.region,'auto');
    assert.equal(config.endpoint,'https://s3.gregale.test');
    assert.equal({...config}.credentials.accessKeyId,'private-access');
    assert.ok(!JSON.stringify(config).includes('private-'));
    assert.ok(!inspect(config).includes('private-'));
    if(config.credentials.secretAccessKey === 'private-rejected') throw new Error('private-driver-error');
    return {key:config.credentials.secretAccessKey};
  });
  assert.throws(()=>b.current());
  await b.reload(values());
  const old = b.current();
  await b.reload({...values(),ASSETS_SECRET_ACCESS_KEY:'private-rotated'});
  assert.equal(b.current().client.key,'private-rotated');
  assert.equal(b.current().bucket,'assets');
  assert.equal(old.client.key,'private-secret');
  await assert.rejects(b.reload({...values(),ASSETS_SECRET_ACCESS_KEY:'private-rejected'}), e=>e instanceof Error && !String(e).includes('private-'));
  assert.throws(()=>b.current());
  await assert.rejects(b.reload({...values(),ASSETS_SECRET_ACCESS_KEY:''}));
});

test('serialized candidates cannot overwrite a newer client; cancellation cannot apply',async()=>{
  let release!:()=>void;
  const hold = new Promise<void>(resolve=>{release=resolve;});
  const b = new ObjectStorageBinding('ASSETS',async config=>{
    if(config.credentials.secretAccessKey === 'private-secret') await hold;
    return config.credentials.secretAccessKey;
  });
  const input = values();
  const first=b.reload(input);
  input.ASSETS_SECRET_ACCESS_KEY='private-mutated';
  const second=b.reload({...values(),ASSETS_SECRET_ACCESS_KEY:'private-new'});
  release();await Promise.all([first,second]);
  assert.equal(b.current().client,'private-new');
  const aborted=AbortSignal.abort();
  await assert.rejects(b.reload(values(),aborted));
  assert.throws(()=>b.current());
});

test('invalid binding configuration cannot invoke a factory',async()=>{
  for(const [key,value] of [['ASSETS_ENDPOINT','https://user:private-secret@example.test'],['ASSETS_ENDPOINT','https://example.test?secret=x'],['ASSETS_BUCKET','../other'],['ASSETS_REGION','us east'],['ASSETS_ADDRESSING_STYLE','virtual']]){
    const b=new ObjectStorageBinding('ASSETS',()=>{throw new Error('factory should not run');});
    await assert.rejects(b.reload({...values(),[key!]:value!}),/configuration is invalid/);
  }
  assert.throws(()=>new ObjectStorageBinding('invalid-prefix',()=>null));
});

test('atomic secret reload ACK follows client adoption; failed candidates receive only a failed ACK',async()=>{
  const {applyLatestSecretSnapshot}=await import(new URL('../../../../cmd/gregale/templates/secret-reload-node/secret-reload.js',import.meta.url).href);
  const binding=new ObjectStorageBinding('ASSETS',async config=>{
    if(config.credentials.secretAccessKey==='private-rejected') throw new Error('private-driver-error');
    return config.credentials.secretAccessKey;
  });
  const generation='b'.repeat(32),revision='a'.repeat(64);
  const env={FAAS_SECRETS_SNAPSHOT_FILE:'/snapshot',FAAS_SECRETS_RELOAD_ACK_ENDPOINT:'http://local/ack',FAAS_SECRETS_RELOAD_GENERATION:generation};
  const receipts: unknown[]=[];
  await applyLatestSecretSnapshot({env,readFile:async()=>JSON.stringify({revision,secrets:values()}),
    apply:async(snapshot: {secrets:Record<string,string>})=>binding.reload(snapshot.secrets),
    fetchImpl:async(_url:unknown,request:{body:string})=>{
      assert.equal(binding.current().client,'private-secret');
      receipts.push(JSON.parse(request.body));return new Response(null,{status:202});
    }});
  assert.deepEqual(receipts,[{revision,status:'applied',generation}]);
  await assert.rejects(applyLatestSecretSnapshot({env,readFile:async()=>JSON.stringify({revision,secrets:{...values(),ASSETS_SECRET_ACCESS_KEY:'private-rejected'}}),
    apply:async(snapshot:{secrets:Record<string,string>})=>binding.reload(snapshot.secrets),
    fetchImpl:async(_url:unknown,request:{body:string})=>{
      assert.throws(()=>binding.current());receipts.push(JSON.parse(request.body));return new Response(null,{status:202});
    }}),/could not be applied/);
  assert.deepEqual(receipts[1],{revision,status:'failed',generation});
  assert.ok(!JSON.stringify(receipts).includes('private-'));
});
