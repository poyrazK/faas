import test from 'node:test';
import assert from 'node:assert/strict';

import { Component, createElement, type ReactNode } from 'react';
import TestRenderer, { act, type ReactTestRenderer } from 'react-test-renderer';

import { createRealtimeClient, REALTIME_RESUME_SUBPROTOCOL, type RealtimeSocket } from '../src/preview.js';
import { RealtimeProvider, useOccupancy, usePresence, useRealtimeChannel, useRealtimeClient } from '../src/react.js';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

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
    if (Date.now() > deadline) throw new Error('timed out waiting for the realtime hooks');
    await new Promise<void>((resolve) => setTimeout(resolve, 1));
  }
}

test('hooks render a channel view and release the channel on unmount', async () => {
  const sockets: FakeSocket[] = [];
  const client = createRealtimeClient({
    url: 'wss://app.example.test/__gregale/realtime/endpoint-1', lingerMs: 0,
    webSocketFactory: async () => { const socket = new FakeSocket(); sockets.push(socket); queueMicrotask(() => socket.open()); return socket; },
  });
  const renders: Array<{ status: string; messages: number; members: number; watching?: number }> = [];
  function Room() {
    const view = useRealtimeChannel('lobby', { presence: { name: 'ada' } });
    const members = usePresence('lobby');
    const watching = useOccupancy('lobby');
    renders.push({ status: view.status, messages: view.messages.length, members: members.length, watching });
    return null;
  }
  let renderer!: ReactTestRenderer;
  await act(async () => {
    renderer = TestRenderer.create(createElement(RealtimeProvider, { client }, createElement(Room)));
  });
  await act(async () => { await until(() => sockets[0]?.sent.length === 1); });
  const socket = sockets[0]!;
  assert.deepEqual(socket.sent[0], { type: 'subscribe', channel: 'lobby', after: 0, state: { name: 'ada' }, occupancy: true },
    'useOccupancy in the same render turns occupancy on');
  await act(async () => {
    socket.receive({ type: 'subscribed', channel: 'lobby', sequence: 0 });
    socket.receive({ type: 'presence', channel: 'lobby', event: 'snapshot', complete: true,
      members: [{ member_id: 'm1', state: { name: 'ada' }, connection_count: 1 }] });
    socket.receive({ type: 'occupancy', channel: 'lobby', subscribers: 2 });
    socket.receive({ type: 'message', channel: 'lobby', sequence: 1, message_id: 'a', data_base64: 'aGk=' });
    await until(() => client.channel('lobby').getView().messages.length === 1);
  });
  assert.deepEqual(renders.at(-1), { status: 'live', messages: 1, members: 1, watching: 2 });
  assert.equal(renders[0]?.status, 'idle', 'the first render shows the idle view');

  await act(async () => { renderer.unmount(); });
  await until(() => JSON.stringify(socket.sent.at(-1)) === JSON.stringify({ type: 'unsubscribe', channel: 'lobby' }));
  client.close();
});

class Boundary extends Component<{ children?: ReactNode; onError(error: unknown): void }, { failed: boolean }> {
  override state = { failed: false };
  static getDerivedStateFromError() { return { failed: true }; }
  override componentDidCatch(error: unknown) { this.props.onError(error); }
  override render() { return this.state.failed ? null : this.props.children; }
}

test('hooks without a provider explain what is missing', async () => {
  function Orphan() {
    useRealtimeClient();
    return null;
  }
  const errors: unknown[] = [];
  const original = console.error;
  console.error = () => {}; // React logs the render error it hands to the boundary
  try {
    await act(async () => {
      TestRenderer.create(createElement(Boundary, { onError: (error) => errors.push(error) }, createElement(Orphan)));
    });
  } finally {
    console.error = original;
  }
  assert.match(String(errors[0]), /RealtimeProvider/);
});
