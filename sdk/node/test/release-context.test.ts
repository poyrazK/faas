import test from 'node:test';
import assert from 'node:assert/strict';

import {
  createGregaleFetch,
  currentGregaleRelease,
  currentGregaleRequestDeadline,
  GREGALE_RELEASE_HEADER,
  GREGALE_REVISION_HEADER,
  GREGALE_REQUEST_DEADLINE_HEADER,
  gregaleReleaseMetaTag,
  withGregaleReleaseContext,
  withGregaleRequestContext,
} from '../src/index.js';

test('SSR release meta helper renders only a validated release ID', () => {
  const release = 'a91f2000-0000-4000-8000-000000000001';
  assert.equal(
    gregaleReleaseMetaTag(new Headers({ [GREGALE_RELEASE_HEADER]: release })),
    `<meta name="gregale-release" content="${release}">`,
  );
  assert.equal(gregaleReleaseMetaTag(new Headers()), '');
  assert.equal(gregaleReleaseMetaTag(new Headers({ [GREGALE_RELEASE_HEADER]: '<script>' })), '');
  assert.equal(gregaleReleaseMetaTag([
    [GREGALE_RELEASE_HEADER, release],
    [GREGALE_RELEASE_HEADER, 'another-release'],
  ]), '');
});

test('request deadline wins on managed calls and is stripped from external calls', async () => {
  const seen: Headers[] = [];
  const redirects: Array<RequestRedirect | undefined> = [];
  const fetcher = createGregaleFetch(async (_input, init) => {
    seen.push(new Headers(init?.headers));
    redirects.push(init?.redirect);
    return new Response(null, { status: 204 });
  });
  const deadline = 'v1.key.root.signature';
  await withGregaleRequestContext({ [GREGALE_REQUEST_DEADLINE_HEADER]: deadline }, async () => {
    assert.equal(currentGregaleRequestDeadline(), deadline);
    await fetcher('http://billing.svc.gregale/', { redirect: 'follow', headers: { [GREGALE_REQUEST_DEADLINE_HEADER]: 'v1.key.override.signature' } });
    await fetcher('http://billing.internal/');
    await fetcher(new Request('https://payments.example.test/', { headers: { [GREGALE_REQUEST_DEADLINE_HEADER]: deadline, 'X-Customer': 'preserved' } }));
  });
  assert.equal(seen[0]?.get(GREGALE_REQUEST_DEADLINE_HEADER), deadline);
  assert.equal(seen[1]?.get(GREGALE_REQUEST_DEADLINE_HEADER), deadline);
  assert.equal(seen[2]?.has(GREGALE_REQUEST_DEADLINE_HEADER), false);
  assert.equal(seen[2]?.get('X-Customer'), 'preserved');
  assert.equal(redirects[0], 'manual');
  assert.equal(redirects[1], 'manual');
  assert.equal(currentGregaleRequestDeadline(), undefined);
});

test('request deadlines stay isolated across concurrent handlers and ambiguous carriers', async () => {
  const fetcher = createGregaleFetch(async (_input, init) => new Response(new Headers(init?.headers).get(GREGALE_REQUEST_DEADLINE_HEADER)));
  const call = (deadline: string) => withGregaleRequestContext({ [GREGALE_REQUEST_DEADLINE_HEADER]: deadline }, async () => {
    await new Promise((resolve) => setTimeout(resolve, deadline.includes('first') ? 10 : 0));
    return (await fetcher('http://identity.svc.gregale/')).text();
  });
  assert.deepEqual(await Promise.all([call('v1.key.first.signature'), call('v1.key.second.signature')]), ['v1.key.first.signature', 'v1.key.second.signature']);
  await withGregaleRequestContext([[GREGALE_REQUEST_DEADLINE_HEADER, 'one'], [GREGALE_REQUEST_DEADLINE_HEADER, 'two']], () => {
    assert.equal(currentGregaleRequestDeadline(), undefined);
  });
  await assert.rejects(withGregaleRequestContext({ [GREGALE_REQUEST_DEADLINE_HEADER]: 'v1.key.error.signature' }, async () => { throw new Error('handler failed'); }));
  assert.equal(currentGregaleRequestDeadline(), undefined);
});

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
