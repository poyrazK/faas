import test from 'node:test';
import assert from 'node:assert/strict';

import {
  createGregaleBrowserFetch,
  GREGALE_RELEASE_HEADER,
  GREGALE_REVISION_HEADER,
} from '../src/browser.js';

const API_ORIGIN = 'https://api.example.test';
const EXTERNAL_ORIGIN = 'https://third-party.example.test';
const RELEASE_A = 'a91f2000-0000-4000-8000-000000000001';
const RELEASE_B = 'b91f2000-0000-4000-8000-000000000002';

test('captures a release response and pins subsequent requests to managed origins', async () => {
  const calls: Array<{ url: string; headers: Headers }> = [];
  let count = 0;
  const client = createGregaleBrowserFetch({
    managedOrigins: [API_ORIGIN],
    fetch: async (input, init) => {
      calls.push({ url: input instanceof Request ? input.url : input.toString(), headers: new Headers(init?.headers) });
      count += 1;
      return new Response(null, {
        status: 200,
        headers: count === 1 ? { [GREGALE_RELEASE_HEADER]: RELEASE_A } : {},
      });
    },
  });

  const firstHeaders = new Headers({ 'X-Request-Id': 'first' });
  await client.fetch(`${API_ORIGIN}/v1/bootstrap`, { headers: firstHeaders });
  await client.fetch(`${API_ORIGIN}/v1/checkout`);

  assert.equal(client.release, RELEASE_A);
  assert.equal(calls[0]?.headers.has(GREGALE_RELEASE_HEADER), false);
  assert.equal(calls[1]?.headers.get(GREGALE_RELEASE_HEADER), RELEASE_A);
  assert.equal(calls[0]?.headers.get('X-Request-Id'), 'first');
  assert.equal(firstHeaders.has(GREGALE_RELEASE_HEADER), false, 'the caller headers are not mutated');
});

test('serializes unpinned startup requests until the first release is discovered', async () => {
  const calls: Array<{ url: string; headers: Headers }> = [];
  let finishFirst!: (response: Response) => void;
  const client = createGregaleBrowserFetch({
    managedOrigins: [API_ORIGIN],
    fetch: (input, init) => {
      calls.push({ url: input.toString(), headers: new Headers(init?.headers) });
      if (calls.length === 1) {
        return new Promise<Response>((resolve) => {
          finishFirst = resolve;
        });
      }
      return Promise.resolve(new Response(null, { status: 200 }));
    },
  });

  const bootstrap = client.fetch(`${API_ORIGIN}/v1/bootstrap`);
  const concurrentRequest = client.fetch(`${API_ORIGIN}/v1/profile`);
  assert.equal(calls.length, 1, 'only the discovery request leaves before a release is known');

  finishFirst(new Response(null, { status: 200, headers: { [GREGALE_RELEASE_HEADER]: RELEASE_A } }));
  await Promise.all([bootstrap, concurrentRequest]);

  assert.equal(calls.length, 2);
  assert.equal(calls[1]?.headers.get(GREGALE_RELEASE_HEADER), RELEASE_A);
});

test('an aborted startup request does not wait for release discovery', async () => {
  let finishFirst!: (response: Response) => void;
  let calls = 0;
  const client = createGregaleBrowserFetch({
    managedOrigins: [API_ORIGIN],
    fetch: () => {
      calls += 1;
      return new Promise<Response>((resolve) => {
        finishFirst = resolve;
      });
    },
  });

  const bootstrap = client.fetch(`${API_ORIGIN}/v1/bootstrap`);
  const controller = new AbortController();
  const waitingRequest = client.fetch(`${API_ORIGIN}/v1/profile`, { signal: controller.signal })
    .then(() => undefined, (error: unknown) => error);
  controller.abort();

  const error = await waitingRequest;
  assert.equal((error as Error).name, 'AbortError');
  assert.equal(calls, 1, 'the aborted request never reaches the underlying fetch');

  finishFirst(new Response(null, { status: 200, headers: { [GREGALE_RELEASE_HEADER]: RELEASE_A } }));
  await bootstrap;
});

test('does not send or learn release context from external origins', async () => {
  const calls: Array<{ url: string; headers: Headers }> = [];
  const client = createGregaleBrowserFetch({
    managedOrigins: [API_ORIGIN],
    fetch: async (input, init) => {
      calls.push({ url: input.toString(), headers: new Headers(init?.headers) });
      return new Response(null, { status: 200, headers: { [GREGALE_RELEASE_HEADER]: RELEASE_A } });
    },
  });

  await client.fetch(`${EXTERNAL_ORIGIN}/pixel`);
  assert.equal(client.release, undefined);
  await client.fetch(`${API_ORIGIN}/v1/health`);
  await client.fetch(`${EXTERNAL_ORIGIN}/pixel`);

  assert.equal(calls[0]?.headers.has(GREGALE_RELEASE_HEADER), false);
  assert.equal(calls[2]?.headers.has(GREGALE_RELEASE_HEADER), false);
  assert.equal(client.release, RELEASE_A);
});

test('does not combine a caller revision pin with the project release', async () => {
  let sent = new Headers();
  const client = createGregaleBrowserFetch({
    managedOrigins: [API_ORIGIN],
    initialRelease: RELEASE_A,
    fetch: async (_input, init) => {
      sent = new Headers(init?.headers);
      return new Response(null, { status: 200 });
    },
  });
  const callerHeaders = new Headers({ [GREGALE_REVISION_HEADER]: 'deployment-specific-pin' });

  await client.fetch(`${API_ORIGIN}/v1/checkout`, { headers: callerHeaders });

  assert.equal(sent.get(GREGALE_REVISION_HEADER), 'deployment-specific-pin');
  assert.equal(sent.has(GREGALE_RELEASE_HEADER), false);
  assert.equal(callerHeaders.has(GREGALE_RELEASE_HEADER), false);
});

test('returns release-expired responses unchanged and keeps the pin until explicit reset', async () => {
  const sentReleases: Array<string | null> = [];
  const client = createGregaleBrowserFetch({
    managedOrigins: [API_ORIGIN],
    initialRelease: RELEASE_A,
    fetch: async (_input, init) => {
      sentReleases.push(new Headers(init?.headers).get(GREGALE_RELEASE_HEADER));
      return new Response('expired', { status: 410 });
    },
  });

  const expired = await client.fetch(`${API_ORIGIN}/v1/checkout`);
  assert.equal(expired.status, 410);
  assert.equal(await expired.text(), 'expired');
  await client.fetch(`${API_ORIGIN}/v1/checkout`);

  assert.deepEqual(sentReleases, [RELEASE_A, RELEASE_A]);
  assert.equal(client.release, RELEASE_A, 'expiry never silently switches to the active release');

  client.clearRelease();
  assert.equal(client.release, undefined);
});

test('release state is isolated per browser client and a mismatched response cannot replace it', async () => {
  const clientA = createGregaleBrowserFetch({
    managedOrigins: [API_ORIGIN],
    initialRelease: RELEASE_A,
    fetch: async () => new Response(null, { status: 200, headers: { [GREGALE_RELEASE_HEADER]: RELEASE_B } }),
  });
  const clientB = createGregaleBrowserFetch({
    managedOrigins: [API_ORIGIN],
    fetch: async () => new Response(null, { status: 200, headers: { [GREGALE_RELEASE_HEADER]: RELEASE_B } }),
  });

  await clientA.fetch(`${API_ORIGIN}/v1/checkout`);
  await clientB.fetch(`${API_ORIGIN}/v1/bootstrap`);

  assert.equal(clientA.release, RELEASE_A);
  assert.equal(clientB.release, RELEASE_B);
});

test('requires an absolute initial release ID and managed origins', () => {
  assert.throws(() => createGregaleBrowserFetch({
    managedOrigins: [API_ORIGIN],
    initialRelease: 'not-a-release',
    fetch: globalThis.fetch,
  }), /initialRelease must be a Gregale release UUID/);
  assert.throws(() => createGregaleBrowserFetch({
    managedOrigins: ['file:///etc/passwd'],
    fetch: globalThis.fetch,
  }), /must use HTTP or HTTPS/);
});
