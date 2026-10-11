import test from 'node:test';
import assert from 'node:assert/strict';
import { OpenAPI, StorageService } from '../src/generated/index.js';

test('historical object reads and version pagination preserve public selectors', async () => {
  const oldFetch = globalThis.fetch, oldBase = OpenAPI.BASE, oldToken = OpenAPI.TOKEN;
  const key = '目录 /+%.txt', version = '11111111-1111-4111-8111-111111111111';
  OpenAPI.BASE = 'https://api.example.test'; OpenAPI.TOKEN = 'token';
  globalThis.fetch = async (input, init) => {
    const url = new URL(String(input));
    assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer token');
    if (init?.method === 'POST') {
      assert.deepEqual(JSON.parse(String(init.body)), { method: 'GET', key, version_id: version });
      return new Response(JSON.stringify({url:'https://s3.gregale.dev/assets/file',method:'GET',headers:{},expires_at:'2026-10-07T00:00:00Z'}), {headers:{'Content-Type':'application/json'}});
    }
    assert.equal(url.pathname, '/v1/apps/demo/buckets/bucket/objects/versions');
    assert.equal(url.searchParams.get('key_marker'), key);
    assert.equal(url.searchParams.get('version_id_marker'), version);
    assert.equal(url.searchParams.get('prefix'), '目录');
    assert.equal(url.searchParams.get('limit'), '1');
    return new Response(JSON.stringify({items:[{key,version_id:version,is_latest:false,delete_marker:false,size_bytes:3,last_modified:'2026-10-07T00:00:00Z'}],common_prefixes:[],next_key_marker:key,next_version_id_marker:version}), {headers:{'Content-Type':'application/json'}});
  };
  try {
    await StorageService.signBucketObject({slug:'demo',bucket:'bucket',requestBody:{method:'GET',key,version_id:version}});
    const page = await StorageService.listObjectBucketVersions({slug:'demo',bucket:'bucket',prefix:'目录',keyMarker:key,versionIdMarker:version,limit:1});
    assert.ok('items' in page);
    assert.equal(page.items[0]?.version_id, version);
    assert.equal(page.next_key_marker, key); assert.equal(page.next_version_id_marker, version);
  } finally { globalThis.fetch=oldFetch; OpenAPI.BASE=oldBase; OpenAPI.TOKEN=oldToken; }
});

test('signed object requests preserve owned encryption and receipt identity', async () => {
  const oldFetch = globalThis.fetch;
  const oldBase = OpenAPI.BASE;
  const oldToken = OpenAPI.TOKEN;
  const encryption = { algorithm: 'aws:kms' as const, key_id: 'arn:gregale:kms:us-east-1:11111111-1111-4111-8111-111111111111:key/22222222-2222-4222-8222-222222222222', bucket_key_enabled: false };
  OpenAPI.BASE = 'https://api.example.test';
  OpenAPI.TOKEN = 'token';
  globalThis.fetch = async (input, init) => {
    assert.equal(String(input), 'https://api.example.test/v1/apps/demo/buckets/bucket/signed-url');
    assert.equal(init?.method, 'POST');
    assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer token');
    assert.deepEqual(JSON.parse(String(init?.body)), {method:'PUT',key:'file',size_bytes:3,encryption});
    return new Response(JSON.stringify({url:'https://s3.gregale.dev/assets/file',method:'PUT',headers:{'X-Amz-Server-Side-Encryption':'aws:kms'},expires_at:'2026-10-04T00:00:00Z',upload_id:'33333333-3333-4333-8333-333333333333'}), {status:200,headers:{'Content-Type':'application/json'}});
  };
  try {
    const out = await StorageService.signBucketObject({slug:'demo',bucket:'bucket',requestBody:{method:'PUT',key:'file',size_bytes:3,encryption}});
    assert.ok('upload_id' in out);
    assert.equal(out.upload_id, '33333333-3333-4333-8333-333333333333');
  } finally {
    globalThis.fetch = oldFetch;
    OpenAPI.BASE = oldBase;
    OpenAPI.TOKEN = oldToken;
  }
});

test('control multipart creation freezes owned encryption', async () => {
  const oldFetch = globalThis.fetch;
  const oldBase = OpenAPI.BASE;
  OpenAPI.BASE = 'https://api.example.test';
  const encryption = { algorithm: 'aws:kms' as const, key_id: 'owned-key', bucket_key_enabled: false };
  globalThis.fetch = async (input, init) => {
    assert.equal(String(input), 'https://api.example.test/v1/apps/demo/buckets/bucket/multipart-uploads');
    assert.deepEqual(JSON.parse(String(init?.body)), {key:'file',size_bytes:3,encryption});
    return new Response(JSON.stringify({id:'session',key:'file',size_bytes:3,part_size_bytes:3,part_count:1,content_type:'application/octet-stream',state:'active',expires_at:'2026-10-04T00:00:00Z',created_at:'2026-10-03T23:00:00Z',encryption}), {status:201,headers:{'Content-Type':'application/json'}});
  };
  try {
    const out = await StorageService.createObjectMultipartUpload({slug:'demo',bucket:'bucket',requestBody:{key:'file',size_bytes:3,encryption}});
    assert.ok('encryption' in out);
    assert.deepEqual(out.encryption, encryption);
  } finally {
    globalThis.fetch = oldFetch;
    OpenAPI.BASE = oldBase;
  }
});
