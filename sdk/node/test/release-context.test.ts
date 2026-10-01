import test from 'node:test';
import assert from 'node:assert/strict';

import {
  createGregaleFetch,
  currentGregaleRelease,
  GREGALE_RELEASE_HEADER,
  GREGALE_REVISION_HEADER,
  GREGALE_FLAG_PROPAGATION_HEADER,
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

test('flag context stays on the managed redirect chain and is stripped before an external redirect', async () => {
  const calls: Array<{ url: string; headers: Headers; method: string; body: string; redirect: RequestRedirect }> = [];
  const fetchImpl: typeof fetch = async (input, init) => {
    const request = input instanceof Request ? input : new Request(input, init);
    calls.push({
      url: request.url,
      headers: new Headers(init?.headers ?? request.headers),
      method: request.method,
      body: await request.clone().text(),
      redirect: init?.redirect ?? request.redirect,
    });
    if (calls.length === 1) {
      return new Response(null, { status: 307, headers: { Location: 'https://catalog.svc.gregale/charge' } });
    }
    if (calls.length === 2) {
      return new Response(null, { status: 302, headers: { Location: 'https://payments.example.test/charge' } });
    }
    return new Response(null, { status: 204 });
  };
  const fetcher = createGregaleFetch(fetchImpl, { flags: { propagationHeader: () => 'evaluated-context' } });

  const response = await fetcher('https://billing.svc.gregale/charge', {
    method: 'POST',
    headers: {
      Authorization: 'Bearer caller-token',
      'Content-Type': 'application/json',
      [GREGALE_RELEASE_HEADER]: 'release-123',
    },
    body: '{}',
  });

  assert.equal(response.status, 204);
  assert.equal(calls.length, 3);
  assert.equal(calls[0]?.headers.get(GREGALE_FLAG_PROPAGATION_HEADER), 'evaluated-context');
  assert.equal(calls[0]?.headers.get(GREGALE_RELEASE_HEADER), 'release-123');
  assert.equal(calls[0]?.redirect, 'manual');
  assert.equal(calls[1]?.url, 'https://catalog.svc.gregale/charge');
  assert.equal(calls[1]?.headers.get(GREGALE_FLAG_PROPAGATION_HEADER), 'evaluated-context');
  assert.equal(calls[1]?.headers.get(GREGALE_RELEASE_HEADER), 'release-123');
  assert.equal(calls[1]?.headers.has('authorization'), false);
  assert.equal(calls[1]?.method, 'POST');
  assert.equal(calls[1]?.body, '{}');
  assert.equal(calls[1]?.redirect, 'manual');
  assert.equal(calls[2]?.headers.has(GREGALE_FLAG_PROPAGATION_HEADER), false);
  assert.equal(calls[2]?.headers.has(GREGALE_RELEASE_HEADER), false);
  assert.equal(calls[2]?.headers.has('authorization'), false);
  assert.equal(calls[2]?.method, 'GET');
  assert.equal(calls[2]?.body, '');
  assert.equal(calls[2]?.redirect, 'manual');
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
