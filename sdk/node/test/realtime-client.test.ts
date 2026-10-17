import test from 'node:test';
import assert from 'node:assert/strict';

import { createRealtimeClient, REALTIME_RESUME_SUBPROTOCOL, type RealtimeSocket } from '../src/index.js';

class FakeSocket extends EventTarget implements RealtimeSocket {
  protocol = REALTIME_RESUME_SUBPROTOCOL;
  readyState = 0;
  sent: Array<Record<string, unknown>> = [];
  open() { this.readyState = 1; this.dispatchEvent(new Event('open')); }
  send(data: string) {
    if (this.readyState !== 1) throw new Error('socket closed');
    this.sent.push(JSON.parse(data) as Record<string, unknown>);
  }
  receive(frame: Record<string, unknown>) { this.dispatchEvent(new MessageEvent('message', { data: JSON.stringify(frame) })); }
  close() {
    if (this.readyState === 3) return;
    this.readyState = 3;
    this.dispatchEvent(new Event('close'));
  }
}

async function until(check: () => boolean): Promise<void> {
  const deadline = Date.now() + 1_000;
  while (!check()) {
    if (Date.now() > deadline) throw new Error('timed out waiting for the realtime client');
    await new Promise<void>((resolve) => setTimeout(resolve, 1));
  }
}

function fakeServer() {
  const sockets: FakeSocket[] = [];
  return {
    sockets,
    webSocketFactory: async () => {
      const socket = new FakeSocket();
      sockets.push(socket);
      queueMicrotask(() => socket.open());
      return socket;
    },
  };
}

test('watching a channel subscribes it and keeps its view current', async () => {
  const server = fakeServer();
  const client = createRealtimeClient({ url: 'wss://app.example.test/__gregale/realtime/endpoint-1', webSocketFactory: server.webSocketFactory, messageLimit: 2 });
  const lobby = client.channel('lobby', { occupancy: true, presence: { status: 'online' } });
  assert.equal(client.channel('lobby'), lobby, 'one handle per channel');
  assert.equal(lobby.getView().status, 'idle');
  let changes = 0;
  const stop = lobby.subscribe(() => { changes++; });
  assert.equal(lobby.getView().status, 'connecting');
  await until(() => server.sockets[0]?.sent.length === 1);
  const socket = server.sockets[0]!;
  assert.deepEqual(socket.sent[0], { type: 'subscribe', channel: 'lobby', after: 0, state: { status: 'online' }, occupancy: true });

  socket.receive({ type: 'subscribed', channel: 'lobby', sequence: 0 });
  socket.receive({ type: 'presence', channel: 'lobby', event: 'snapshot', complete: true,
    members: [{ member_id: 'm1', state: { status: 'online' }, connection_count: 1 }] });
  socket.receive({ type: 'occupancy', channel: 'lobby', subscribers: 3 });
  for (const sequence of [1, 2, 3]) socket.receive({ type: 'message', channel: 'lobby', sequence, message_id: `id-${sequence}`, data_base64: 'eA==' });
  await until(() => lobby.getView().messages.length === 2 && lobby.getView().messages[1]?.sequence === 3);
  const view = lobby.getView();
  assert.equal(view.status, 'live');
  assert.deepEqual(view.messages.map((message) => message.sequence), [2, 3], 'messageLimit keeps the most recent');
  assert.deepEqual(view.presence.map((member) => member.memberId), ['m1']);
  assert.equal(view.occupancy, 3);
  assert.equal(lobby.getView(), view, 'the view object is stable until something changes');
  assert.ok(changes >= 5);

  lobby.updatePresence({ status: 'typing' });
  assert.deepEqual(socket.sent.at(-1), { type: 'presence', channel: 'lobby', state: { status: 'typing' } });
  stop();
  client.close();
  assert.equal(lobby.getView().status, 'closed');
});

test('the last watcher leaving unsubscribes after the linger and closes the idle socket', async () => {
  const server = fakeServer();
  const client = createRealtimeClient({ url: 'wss://app.example.test/__gregale/realtime/endpoint-1', webSocketFactory: server.webSocketFactory, lingerMs: 20 });
  const lobby = client.channel('lobby');
  const first = lobby.subscribe(() => {});
  await until(() => server.sockets[0]?.sent.length === 1);
  server.sockets[0]!.receive({ type: 'subscribed', channel: 'lobby', sequence: 0 });
  await until(() => lobby.getView().status === 'live');
  // A watcher that comes back within the linger keeps the subscription.
  first();
  const second = lobby.subscribe(() => {});
  await new Promise((resolve) => setTimeout(resolve, 40));
  assert.equal(server.sockets[0]!.sent.length, 1, 'no resubscribe while watched');
  second();
  await until(() => lobby.getView().status === 'idle');
  assert.deepEqual(server.sockets[0]!.sent.at(-1), { type: 'unsubscribe', channel: 'lobby' });
  assert.equal(server.sockets[0]!.readyState, 3, 'the idle connection closes');
  client.close();
});

test('a refused channel shows its error while other channels continue', async () => {
  const server = fakeServer();
  const client = createRealtimeClient({ url: 'wss://app.example.test/__gregale/realtime/endpoint-1', webSocketFactory: server.webSocketFactory });
  const secret = client.channel('secret');
  const lobby = client.channel('lobby');
  secret.subscribe(() => {});
  lobby.subscribe(() => {});
  await until(() => server.sockets[0]?.sent.length === 2);
  server.sockets[0]!.receive({ type: 'error', channel: 'secret', code: 'not_authorized' });
  server.sockets[0]!.receive({ type: 'subscribed', channel: 'lobby', sequence: 0 });
  await until(() => lobby.getView().status === 'live' && secret.getView().status === 'error');
  assert.equal(secret.getView().error, 'not_authorized');
  client.close();
});

test('a dropped connection shows reconnecting and resumes from the last message', async () => {
  const server = fakeServer();
  const client = createRealtimeClient({ url: 'wss://app.example.test/__gregale/realtime/endpoint-1', webSocketFactory: server.webSocketFactory, retryInitialMs: 0, retryMaxMs: 0 });
  const lobby = client.channel('lobby');
  const statuses: string[] = [];
  lobby.subscribe(() => { if (statuses.at(-1) !== lobby.getView().status) statuses.push(lobby.getView().status); });
  await until(() => server.sockets[0]?.sent.length === 1);
  server.sockets[0]!.receive({ type: 'subscribed', channel: 'lobby', sequence: 0 });
  server.sockets[0]!.receive({ type: 'message', channel: 'lobby', sequence: 1, message_id: 'a' });
  await until(() => lobby.getView().messages.length === 1);
  server.sockets[0]!.close();
  await until(() => server.sockets[1]?.sent.length === 1);
  assert.deepEqual(server.sockets[1]!.sent[0], { type: 'subscribe', channel: 'lobby', after: 1 });
  server.sockets[1]!.receive({ type: 'subscribed', channel: 'lobby', sequence: 1 });
  await until(() => lobby.getView().status === 'live');
  assert.deepEqual(statuses, ['connecting', 'live', 'reconnecting', 'live']);
  assert.equal(lobby.getView().messages.length, 1, 'messages survive a reconnect');
  client.close();
});

test('a client watches at most eight channels at once', async () => {
  const server = fakeServer();
  const client = createRealtimeClient({ url: 'wss://app.example.test/__gregale/realtime/endpoint-1', webSocketFactory: server.webSocketFactory });
  for (let i = 0; i < 8; i++) client.channel(`c${i}`).subscribe(() => {});
  const ninth = client.channel('c8');
  ninth.subscribe(() => {});
  assert.equal(ninth.getView().status, 'error');
  assert.match(ninth.getView().error ?? '', /at most 8 channels/);
  client.close();
});
