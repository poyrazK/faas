import test from 'node:test';
import assert from 'node:assert/strict';

import {
  createGregaleFetch,
  currentGregaleRelease,
  GREGALE_RELEASE_HEADER,
  GREGALE_REVISION_HEADER,
  withGregaleReleaseContext,
  withGregaleRequestContext,
} from '../src/index.js';

test('request context propagates the release only to managed service calls', async () => {
  const calls: Array<{ url: string; headers: Headers }> = [];
  const fetcher = createGregaleFetch(async (input, init) => {
    calls.push({
      url: input instanceof Request ? input.url : input.toString(),
      headers: new Headers(init?.headers),
    });
    return new Response(null, { status: 204 });
  });

  await withGregaleRequestContext(new Headers({ [GREGALE_RELEASE_HEADER]: 'release-182' }), async () => {
    await fetcher('http://billing.svc.gregale:10080/charge', {
      headers: { [GREGALE_REVISION_HEADER]: 'api-deployment' },
    });
    await fetcher('https://payments.example.test/charge', {
      headers: { [GREGALE_REVISION_HEADER]: 'client-session-pin' },
    });
  });

  assert.equal(calls[0]?.headers.get(GREGALE_RELEASE_HEADER), 'release-182');
  assert.equal(calls[0]?.headers.has(GREGALE_REVISION_HEADER), false);
  assert.equal(calls[1]?.headers.has(GREGALE_RELEASE_HEADER), false);
  assert.equal(calls[1]?.headers.get(GREGALE_REVISION_HEADER), 'client-session-pin');
});

test('an explicit downstream release wins and ambiguous context is ignored', async () => {
  const seen: Array<Headers> = [];
  const fetcher = createGregaleFetch(async (_input, init) => {
    seen.push(new Headers(init?.headers));
    return new Response(null, { status: 204 });
  });

  await withGregaleReleaseContext('ambient-release', async () => {
    await fetcher('http://billing.svc.gregale/', {
      headers: { [GREGALE_RELEASE_HEADER]: 'explicit-release' },
    });
  });
  await withGregaleRequestContext([
    [GREGALE_RELEASE_HEADER, 'release-a'],
    [GREGALE_RELEASE_HEADER, 'release-b'],
  ], async () => {
    assert.equal(currentGregaleRelease(), undefined);
    await fetcher('http://billing.svc.gregale/');
  });

  assert.equal(seen[0]?.get(GREGALE_RELEASE_HEADER), 'explicit-release');
  assert.equal(seen[1]?.has(GREGALE_RELEASE_HEADER), false);
});

test('release context stays isolated between concurrent handlers', async () => {
  const fetcher = createGregaleFetch(async (_input, init) => {
    return new Response(new Headers(init?.headers).get(GREGALE_RELEASE_HEADER), { status: 200 });
  });
  const call = (release: string) => withGregaleReleaseContext(release, async () => {
    await new Promise((resolve) => setTimeout(resolve, release === 'release-a' ? 10 : 0));
    const response = await fetcher('http://identity.svc.gregale/whoami');
    return response.text();
  });

  assert.deepEqual(await Promise.all([call('release-a'), call('release-b')]), ['release-a', 'release-b']);
});
