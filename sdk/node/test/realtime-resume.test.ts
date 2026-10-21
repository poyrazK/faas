import test from 'node:test';
import assert from 'node:assert/strict';

import {
  consumeRealtimeChannel,
  consumeRealtimeChannels,
  REALTIME_RESUME_SUBPROTOCOL,
  RealtimeProtocolError,
  RealtimeResyncRequiredError,
  type RealtimeChannelController,
  type RealtimeMessage,
  type RealtimeSocket,
} from '../src/index.js';

class FakeSocket extends EventTarget implements RealtimeSocket {
  protocol = REALTIME_RESUME_SUBPROTOCOL;
  readyState = 0;
  sent: Array<Record<string, unknown>> = [];

  open() {
    this.readyState = 1;
    this.dispatchEvent(new Event('open'));
  }

  send(data: string) {
    if (this.readyState !== 1) throw new Error('socket closed');
    this.sent.push(JSON.parse(data) as Record<string, unknown>);
  }

  receive(frame: Record<string, unknown>) {
    this.dispatchEvent(new MessageEvent('message', { data: JSON.stringify(frame) }));
  }

  close() {
    if (this.readyState === 3) return;
    this.readyState = 3;
    this.dispatchEvent(new Event('close'));
  }
}

async function until(check: () => boolean): Promise<void> {
  const deadline = Date.now() + 1_000;
  while (!check()) {
    if (Date.now() > deadline) throw new Error('timed out waiting for realtime consumer');
    await new Promise<void>((resolve) => setTimeout(resolve, 1));
  }
}

test('reconnects from a durably processed cursor and acknowledges after save', async () => {
  let cursor = 812;
  const actions: string[] = [];
  const sockets: FakeSocket[] = [];
  const controller = new AbortController();
  const seen: RealtimeMessage[] = [];
  const consuming = consumeRealtimeChannel({
    url: 'wss://app.example.test/__gregale/realtime/endpoint-1',
    channel: 'updates', signal: controller.signal,
    retryInitialMs: 0, retryMaxMs: 0,
    cursorStore: {
      load: () => cursor,
      save: (next) => { actions.push(`save:${next}`); cursor = next; },
    },
    onMessage: (message) => { actions.push(`process:${message.sequence}`); seen.push(message); },
    webSocketFactory: async (_url, protocols) => {
      assert.deepEqual(protocols, [REALTIME_RESUME_SUBPROTOCOL]);
      const socket = new FakeSocket();
      sockets.push(socket);
      queueMicrotask(() => socket.open());
      return socket;
    },
  });

  await until(() => sockets[0]?.sent.length === 1);
  assert.deepEqual(sockets[0]?.sent[0], { type: 'subscribe', channel: 'updates', after: 812 });
  sockets[0]!.receive({ type: 'subscribed', channel: 'updates', sequence: 812 });
  sockets[0]!.receive({ type: 'message', channel: 'updates', sequence: 813,
    message_id: 'id-813', data_base64: 'b25l' });
  await until(() => sockets[0]?.sent.length === 2);
  assert.deepEqual(actions, ['process:813', 'save:813']);
  assert.deepEqual(sockets[0]?.sent[1], { type: 'ack', channel: 'updates', sequence: 813 });
  assert.equal(Buffer.from(seen[0]!.data).toString(), 'one');
  assert.equal(seen[0]?.binary, false, 'omitted binary flag means false');

  sockets[0]!.close();
  await until(() => sockets[1]?.sent.length === 1);
  assert.deepEqual(sockets[1]?.sent[0], { type: 'subscribe', channel: 'updates', after: 813 });
  sockets[1]!.receive({ type: 'subscribed', channel: 'updates', sequence: 813 });
  sockets[1]!.receive({ type: 'message', channel: 'updates', sequence: 814,
    message_id: 'id-814', binary: true });
  await until(() => sockets[1]?.sent.length === 2);
  assert.deepEqual(actions, ['process:813', 'save:813', 'process:814', 'save:814']);
  assert.equal(seen[1]?.data.length, 0, 'omitted empty payload decodes');
  assert.equal(seen[1]?.binary, true);
  controller.abort();
  await consuming;
  assert.equal(cursor, 814);
});

test('does not acknowledge or advance when application processing fails', async () => {
  let cursor = 2;
  const socket = new FakeSocket();
  const consuming = consumeRealtimeChannel({
    url: 'wss://app.example.test/__gregale/realtime/endpoint-1',
    channel: 'updates',
    cursorStore: { load: () => cursor, save: (next) => { cursor = next; } },
    onMessage: () => { throw new Error('database transaction failed'); },
    webSocketFactory: () => { queueMicrotask(() => socket.open()); return socket; },
  });
  await until(() => socket.sent.length === 1);
  socket.receive({ type: 'subscribed', channel: 'updates', sequence: 2 });
  socket.receive({ type: 'message', channel: 'updates', sequence: 3,
    message_id: 'id-3', data_base64: 'dGhyZWU=' });
  await assert.rejects(consuming, /database transaction failed/);
  assert.equal(cursor, 2);
  assert.equal(socket.sent.length, 1);
});

test('does not acknowledge when cursor persistence fails', async () => {
  const socket = new FakeSocket();
  let processed = false;
  const consuming = consumeRealtimeChannel({
    url: 'wss://app.example.test/__gregale/realtime/endpoint-1',
    channel: 'updates',
    cursorStore: { load: () => 2, save: () => { throw new Error('cursor database unavailable'); } },
    onMessage: () => { processed = true; },
    webSocketFactory: () => { queueMicrotask(() => socket.open()); return socket; },
  });
  await until(() => socket.sent.length === 1);
  socket.receive({ type: 'subscribed', channel: 'updates', sequence: 2 });
  socket.receive({ type: 'message', channel: 'updates', sequence: 3,
    message_id: 'id-3', data_base64: 'dGhyZWU=' });
  await assert.rejects(consuming, /cursor database unavailable/);
  assert.equal(processed, true);
  assert.equal(socket.sent.length, 1);
});

test('surfaces an expired cursor without automatically skipping lost history', async () => {
  const socket = new FakeSocket();
  const consuming = consumeRealtimeChannel({
    url: 'wss://app.example.test/__gregale/realtime/endpoint-1',
    channel: 'updates',
    cursorStore: { load: () => 812, save: () => { throw new Error('unexpected save'); } },
    onMessage: () => { throw new Error('unexpected message'); },
    webSocketFactory: () => { queueMicrotask(() => socket.open()); return socket; },
  });
  await until(() => socket.sent.length === 1);
  socket.receive({ type: 'resync_required', channel: 'updates', oldest_sequence: 815, latest_sequence: 820 });
  await assert.rejects(consuming, (error: unknown) => {
    assert.ok(error instanceof RealtimeResyncRequiredError);
    assert.equal(error.oldestSequence, 815);
    assert.equal(error.latestSequence, 820);
    return true;
  });
  assert.equal(socket.sent.length, 1);
});

test('rejects out-of-order delivery rather than persisting a gap', async () => {
  const socket = new FakeSocket();
  const consuming = consumeRealtimeChannel({
    url: 'wss://app.example.test/__gregale/realtime/endpoint-1',
    channel: 'updates',
    cursorStore: { load: () => 1, save: () => { throw new Error('unexpected save'); } },
    onMessage: () => { throw new Error('unexpected message'); },
    webSocketFactory: () => { queueMicrotask(() => socket.open()); return socket; },
  });
  await until(() => socket.sent.length === 1);
  socket.receive({ type: 'subscribed', channel: 'updates', sequence: 1 });
  socket.receive({ type: 'message', channel: 'updates', sequence: 3,
    message_id: 'id-3', data_base64: 'dGhyZWU=' });
  await assert.rejects(consuming, RealtimeProtocolError);
});

test('asks for live occupancy and reports each count', async () => {
  const sockets: FakeSocket[] = [];
  const controller = new AbortController();
  const counts: number[] = [];
  const rejected: string[] = [];
  const consuming = consumeRealtimeChannel({
    url: 'wss://app.example.test/__gregale/realtime/endpoint-1',
    channel: 'lobby', signal: controller.signal, retryInitialMs: 0, retryMaxMs: 0,
    cursorStore: { load: () => 0, save: () => {} },
    onMessage: () => {},
    onOccupancy: ({ channel, subscribers }) => { assert.equal(channel, 'lobby'); counts.push(subscribers); },
    onEphemeralRejected: ({ code }) => { rejected.push(code); },
    webSocketFactory: async () => {
      const socket = new FakeSocket();
      sockets.push(socket);
      queueMicrotask(() => socket.open());
      return socket;
    },
  });
  await until(() => sockets[0]?.sent.length === 1);
  assert.deepEqual(sockets[0]?.sent[0], { type: 'subscribe', channel: 'lobby', after: 0, occupancy: true });
  sockets[0]!.receive({ type: 'subscribed', channel: 'lobby', sequence: 0 });
  sockets[0]!.receive({ type: 'occupancy', channel: 'lobby', subscribers: 23 });
  sockets[0]!.receive({ type: 'occupancy', channel: 'lobby', subscribers: 24 });
  sockets[0]!.receive({ type: 'error', channel: 'lobby', code: 'occupancy_limit' });
  await until(() => counts.length === 2 && rejected.length === 1);
  assert.deepEqual(counts, [23, 24]);
  assert.deepEqual(rejected, ['occupancy_limit']);
  controller.abort();
  await consuming;
});

function memoryCursor(): { load(): number; save(next: number): void } {
  let cursor = 0;
  return { load: () => cursor, save: (next) => { cursor = next; } };
}

test('adds and removes channels on a live connection', async () => {
  const sockets: FakeSocket[] = [];
  const controller = new AbortController();
  const connection: string[] = [];
  const seen: string[] = [];
  let channels!: RealtimeChannelController;
  const consuming = consumeRealtimeChannels({
    url: 'wss://app.example.test/__gregale/realtime/endpoint-1', channels: [],
    signal: controller.signal, retryInitialMs: 0, retryMaxMs: 0,
    controller: (value) => { channels = value; },
    onConnectionChange: (state) => { connection.push(state); },
    webSocketFactory: async () => {
      const socket = new FakeSocket();
      sockets.push(socket);
      queueMicrotask(() => socket.open());
      return socket;
    },
  });
  const onMessage = (message: RealtimeMessage) => { seen.push(`${message.channel}:${message.sequence}`); };
  await until(() => connection.length === 1);
  const socket = sockets[0]!;
  assert.equal(socket.sent.length, 0, 'an empty connection waits for channels');

  await channels.add({ channel: 'a', cursorStore: memoryCursor(), onMessage });
  assert.deepEqual(socket.sent.at(-1), { type: 'subscribe', channel: 'a', after: 0 });
  socket.receive({ type: 'subscribed', channel: 'a', sequence: 0 });
  socket.receive({ type: 'message', channel: 'a', sequence: 1, message_id: 'a1' });
  await until(() => seen.length === 1);
  await channels.add({ channel: 'b', cursorStore: memoryCursor(), onMessage });
  assert.deepEqual(socket.sent.at(-1), { type: 'subscribe', channel: 'b', after: 0 });
  socket.receive({ type: 'subscribed', channel: 'b', sequence: 0 });

  // Frames still in flight for a removed channel are dropped.
  channels.remove('a');
  assert.deepEqual(socket.sent.at(-1), { type: 'unsubscribe', channel: 'a' });
  socket.receive({ type: 'message', channel: 'a', sequence: 2, message_id: 'a2' });
  socket.receive({ type: 'unsubscribed', channel: 'a' });
  // Re-adding before the server confirms waits for that confirmation.
  channels.remove('b');
  await channels.add({ channel: 'b', cursorStore: memoryCursor(), onMessage });
  assert.deepEqual(socket.sent.at(-1), { type: 'unsubscribe', channel: 'b' });
  socket.receive({ type: 'unsubscribed', channel: 'b' });
  await until(() => JSON.stringify(socket.sent.at(-1)) === JSON.stringify({ type: 'subscribe', channel: 'b', after: 0 }));
  socket.receive({ type: 'subscribed', channel: 'b', sequence: 0 });
  socket.receive({ type: 'message', channel: 'b', sequence: 1, message_id: 'b1' });
  await until(() => seen.length === 2);
  assert.deepEqual(seen, ['a:1', 'b:1']);
  assert.deepEqual(channels.channels(), ['b']);

  // A reconnect subscribes the current channels again.
  socket.close();
  await until(() => sockets[1]?.sent.length === 1);
  assert.deepEqual(connection, ['connected', 'disconnected', 'connected']);
  assert.deepEqual(sockets[1]!.sent[0], { type: 'subscribe', channel: 'b', after: 1 });
  await assert.rejects(channels.add({ channel: 'b', cursorStore: memoryCursor(), onMessage }), /duplicate/);
  controller.abort();
  await consuming;
});

test('drops a refused channel and keeps the others', async () => {
  const sockets: FakeSocket[] = [];
  const controller = new AbortController();
  const refused: string[] = [];
  const seen: string[] = [];
  const consuming = consumeRealtimeChannels({
    url: 'wss://app.example.test/__gregale/realtime/endpoint-1',
    signal: controller.signal, retryInitialMs: 0, retryMaxMs: 0,
    channels: [
      { channel: 'secret', cursorStore: memoryCursor(), onMessage: () => {}, onSubscriptionError: (code) => { refused.push(code); } },
      { channel: 'public', cursorStore: memoryCursor(), onMessage: (message) => { seen.push(message.channel); } },
    ],
    webSocketFactory: async () => {
      const socket = new FakeSocket();
      sockets.push(socket);
      queueMicrotask(() => socket.open());
      return socket;
    },
  });
  await until(() => sockets[0]?.sent.length === 2);
  sockets[0]!.receive({ type: 'error', channel: 'secret', code: 'not_authorized' });
  sockets[0]!.receive({ type: 'subscribed', channel: 'public', sequence: 0 });
  sockets[0]!.receive({ type: 'message', channel: 'public', sequence: 1, message_id: 'p1' });
  await until(() => seen.length === 1 && refused.length === 1);
  assert.deepEqual(refused, ['not_authorized']);
  controller.abort();
  await consuming;
});

test('limits a connection to eight channels', async () => {
  const controller = new AbortController();
  let channels!: RealtimeChannelController;
  const consuming = consumeRealtimeChannels({
    url: 'wss://app.example.test/__gregale/realtime/endpoint-1', channels: [],
    signal: controller.signal, controller: (value) => { channels = value; },
    webSocketFactory: async () => { const socket = new FakeSocket(); queueMicrotask(() => socket.open()); return socket; },
  });
  for (let i = 0; i < 8; i++) await channels.add({ channel: `c${i}`, cursorStore: memoryCursor(), onMessage: () => {} });
  await assert.rejects(channels.add({ channel: 'c8', cursorStore: memoryCursor(), onMessage: () => {} }), /up to 8/);
  controller.abort();
  await consuming;
});

test('does not acknowledge a message whose channel was removed during its handler', async () => {
  const sockets: FakeSocket[] = [];
  const controller = new AbortController();
  let channels!: RealtimeChannelController;
  const consuming = consumeRealtimeChannels({
    url: 'wss://app.example.test/__gregale/realtime/endpoint-1', channels: [],
    signal: controller.signal, controller: (value) => { channels = value; },
    webSocketFactory: async () => { const socket = new FakeSocket(); sockets.push(socket); queueMicrotask(() => socket.open()); return socket; },
  });
  await channels.add({ channel: 'a', cursorStore: memoryCursor(), onMessage: () => { channels.remove('a'); } });
  await until(() => sockets[0]?.sent.length === 1);
  sockets[0]!.receive({ type: 'subscribed', channel: 'a', sequence: 0 });
  sockets[0]!.receive({ type: 'message', channel: 'a', sequence: 1, message_id: 'a1' });
  await until(() => sockets[0]!.sent.length === 2);
  await new Promise((resolve) => setTimeout(resolve, 5));
  assert.deepEqual(sockets[0]!.sent.slice(1), [{ type: 'unsubscribe', channel: 'a' }], 'no ack after the unsubscribe');
  controller.abort();
  await consuming;
});
