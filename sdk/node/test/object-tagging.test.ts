import test from 'node:test';
import assert from 'node:assert/strict';
import { OpenAPI, StorageService } from '../src/generated/index.js';

for (const version of [undefined, 'null', 'ad7b43a1-4a3f-4f6d-9518-d1f0e4c79b78']) {
  test(`object tagging preserves the ${version ?? 'current'} selector`, async () => {
    const oldFetch = globalThis.fetch, oldBase = OpenAPI.BASE, oldToken = OpenAPI.TOKEN;
    const key = '目录 /+%.txt', tags = {'team name': 'old & value'};
    const methods: string[] = [];
    OpenAPI.BASE = 'https://api.example.test'; OpenAPI.TOKEN = 'token';
    globalThis.fetch = async (input, init) => {
      const url = new URL(String(input));
      assert.equal(url.pathname, '/v1/apps/demo/buckets/bucket/objects/tags');
      assert.equal(url.searchParams.get('key'), key);
      assert.equal(url.searchParams.get('version_id'), version ?? null);
      assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer token');
      methods.push(init!.method!);
      if (init?.method === 'PUT') assert.deepEqual(JSON.parse(String(init.body)), {tags});
      return new Response(JSON.stringify({...(version ? {version_id: version} : {}), tags: init?.method === 'DELETE' ? {} : tags}), {status: 200, headers: {'Content-Type': 'application/json'}});
    };
    try {
      const input = {slug: 'demo', bucket: 'bucket', key, versionId: version};
      const read = await StorageService.getObjectBucketTags(input);
      assert.ok('tags' in read); assert.deepEqual(read.tags, tags);
      const written = await StorageService.putObjectBucketTags({...input, requestBody: {tags}});
      assert.ok('tags' in written); assert.equal(written.version_id, version);
      const cleared = await StorageService.deleteObjectBucketTags(input);
      assert.ok('tags' in cleared); assert.deepEqual(cleared.tags, {});
      assert.deepEqual(methods, ['GET', 'PUT', 'DELETE']);
    } finally {
      globalThis.fetch = oldFetch; OpenAPI.BASE = oldBase; OpenAPI.TOKEN = oldToken;
    }
  });
}
