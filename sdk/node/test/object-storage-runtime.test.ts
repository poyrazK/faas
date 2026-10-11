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

test('invalidation fences queued and late factories without retiring acquired clients', async () => {
  let release!: () => void;
  let entered!: () => void;
  const held = new Promise<void>(resolve => {release=resolve;});
  const started = new Promise<void>(resolve => {entered=resolve;});
  let calls=0;
  const b = new ObjectStorageBinding('ASSETS', async config => {
    calls++;
    if(config.credentials.secretAccessKey==='private-late') {entered(); await held;}
    return config.credentials.secretAccessKey;
  });
  await b.reload(values());
  const acquired=b.current();
  const late=b.reload({...values(),ASSETS_SECRET_ACCESS_KEY:'private-late'});
  await started;
  const queued=b.reload(values());
  b.invalidate();
  assert.throws(()=>b.current());
  release();
  await assert.rejects(late,/cancelled/);
  await assert.rejects(queued,/cancelled/);
  assert.equal(calls,2);
  assert.equal(acquired.client,'private-secret');
  assert.throws(()=>b.current());
  await b.reload({...values(),ASSETS_SECRET_ACCESS_KEY:'private-recovered'});
  assert.equal(b.current().client,'private-recovered');
});
