import test from 'node:test';
import assert from 'node:assert/strict';

import {
  createBrowserRealtimeSocketFactory,
  REALTIME_RESUME_BEARER_SUBPROTOCOL_PREFIX,
  REALTIME_RESUME_SUBPROTOCOL,
  type RealtimeSocket,
} from '../src/browser.js';

const socket: RealtimeSocket = {
  protocol: REALTIME_RESUME_SUBPROTOCOL,
  readyState: 0,
  addEventListener: () => {},
  removeEventListener: () => {},
  send: () => {},
  close: () => {},
};

test('browser factory refreshes the JWT on each reconnect without mutating protocols', async () => {
  const calls: Array<{ url: string; protocols: string[] }> = [];
  let count = 0;
  const factory = createBrowserRealtimeSocketFactory(
    async () => `aaa.bbb.sig${++count}`,
    (url, protocols) => { calls.push({ url, protocols }); return socket; },
  );
  const protocols = [REALTIME_RESUME_SUBPROTOCOL];
  assert.equal(await factory('wss://app.example.test/__gregale/realtime/endpoint-1', protocols), socket);
  assert.equal(await factory('wss://app.example.test/__gregale/realtime/endpoint-1', protocols), socket);
  assert.deepEqual(protocols, [REALTIME_RESUME_SUBPROTOCOL]);
  assert.deepEqual(calls.map((call) => call.protocols), [
    [REALTIME_RESUME_SUBPROTOCOL, `${REALTIME_RESUME_BEARER_SUBPROTOCOL_PREFIX}aaa.bbb.sig1`],
    [REALTIME_RESUME_SUBPROTOCOL, `${REALTIME_RESUME_BEARER_SUBPROTOCOL_PREFIX}aaa.bbb.sig2`],
  ]);
});

test('browser factory rejects malformed and oversized JWTs before opening a socket', async () => {
  let opened = false;
  for (const token of ['', 'not-a-jwt', 'a.b.c=d', `${'a'.repeat(3070)}.b.c`]) {
    const factory = createBrowserRealtimeSocketFactory(
      () => token,
      () => { opened = true; return socket; },
    );
    await assert.rejects(factory('wss://app.example.test', [REALTIME_RESUME_SUBPROTOCOL]), TypeError);
  }
  assert.equal(opened, false);
});
