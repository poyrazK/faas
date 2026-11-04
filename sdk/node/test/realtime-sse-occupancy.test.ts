import test from 'node:test';
import assert from 'node:assert/strict';

import { subscribeRealtimeSSE, type RealtimeSSEMessage } from '../src/preview.js';

function sseResponse(frames: string[]): Response {
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      for (const frame of frames) controller.enqueue(new TextEncoder().encode(frame));
    },
  });
  return new Response(body, { headers: { 'content-type': 'text/event-stream' } });
}

test('SSE asks for live occupancy and reports counts between messages', async () => {
  const urls: string[] = [];
  const counts: number[] = [];
  const controller = new AbortController();
  const frames = [
    'event: subscribed\ndata: {"channel":"lobby","sequence":"0"}\n\n',
    'event: occupancy\ndata: {"channel":"lobby","subscribers":4}\n\n',
    'event: error\ndata: {"channel":"lobby","code":"occupancy_limit"}\n\n',
    'id: 1\nevent: message\ndata: {"channel":"lobby","sequence":"1","data_base64":"b25l","binary":false}\n\n',
    'event: occupancy\ndata: {"channel":"lobby","subscribers":5}\n\n',
  ];
  const messages: RealtimeSSEMessage[] = [];
  const stream = subscribeRealtimeSSE({
    url: 'https://app.example.test/__gregale/realtime/endpoint-1', channel: 'lobby', signal: controller.signal,
    token: () => 'ticket',
    onOccupancy: ({ channel, subscribers }) => { assert.equal(channel, 'lobby'); counts.push(subscribers); },
    fetch: async (input) => { urls.push(String(input)); return sseResponse(frames); },
  });
  for await (const message of stream) {
    messages.push(message);
    break;
  }
  controller.abort();
  assert.equal(new URL(urls[0]!).searchParams.get('occupancy'), '1');
  assert.deepEqual(counts, [4], 'the count before the message arrived first');
  assert.equal(messages[0]?.sequence, '1');
});

test('SSE without onOccupancy does not ask for counts', async () => {
  const urls: string[] = [];
  const controller = new AbortController();
  const stream = subscribeRealtimeSSE({
    url: 'https://app.example.test/__gregale/realtime/endpoint-1?occupancy=1', channel: 'lobby', signal: controller.signal,
    token: () => 'ticket',
    fetch: async (input) => {
      urls.push(String(input));
      return sseResponse(['id: 1\nevent: message\ndata: {"channel":"lobby","sequence":"1","data_base64":"","binary":false}\n\n']);
    },
  });
  for await (const _ of stream) break;
  controller.abort();
  assert.equal(new URL(urls[0]!).searchParams.has('occupancy'), false);
});
