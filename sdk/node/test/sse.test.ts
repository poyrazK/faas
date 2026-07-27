// test/sse.test.ts — unit tests for the hand-rolled SSE parser.
//
// `parseFrame` is the canonical helper for translating bytes off a
// `text/event-stream` connection into structured `SseEvent` objects.
// The smoke test does not exercise SSE (the fakeapid fixture returns
// JSON, not event-stream), so these tests are the only coverage.
//
// The HTML5 SSE spec (§9.2) defines the wire format:
//   * Lines are separated by LF (CRLF tolerated).
//   * A frame ends with a blank line (\n\n).
//   * `data:` fields are joined with \n (per-event payload).
//   * `:` at the start of a line is a comment, ignored.
//   * Field names are case-sensitive; unknown fields are ignored.

import test from 'node:test';
import assert from 'node:assert/strict';

import { parseFrame } from '../src/sse.js';

test('parseFrame returns null on empty buffer', () => {
  assert.equal(parseFrame(''), null);
});

test('parseFrame returns null when no terminator yet', () => {
  // Incomplete frame — partial data line, no \n\n yet.
  const partial = 'data: hello';
  assert.equal(parseFrame(partial), null);
});

test('parseFrame parses a single data field terminated by \n\n', () => {
  const out = parseFrame('data: hello\n\n');
  assert.ok(out);
  assert.equal(out.event.event, 'message');
  assert.equal(out.event.data, 'hello');
  assert.equal(out.consumed, 'data: hello\n\n'.length);
});

test('parseFrame parses event: + data: together', () => {
  const out = parseFrame('event: update\ndata: {"x":1}\n\n');
  assert.ok(out);
  assert.equal(out.event.event, 'update');
  assert.equal(out.event.data, '{"x":1}');
});

test('parseFrame joins multiple data fields with \\n', () => {
  const out = parseFrame('data: line1\ndata: line2\ndata: line3\n\n');
  assert.ok(out);
  assert.equal(out.event.data, 'line1\nline2\nline3');
});

test('parseFrame preserves id + retry fields', () => {
  const out = parseFrame('id: 42\nretry: 5000\ndata: hello\n\n');
  assert.ok(out);
  assert.equal(out.event.id, '42');
  assert.equal(out.event.retry, 5000);
  assert.equal(out.event.data, 'hello');
});

test('parseFrame ignores comment lines starting with :', () => {
  const out = parseFrame(': this is a heart-beat\ndata: real\n\n');
  assert.ok(out);
  assert.equal(out.event.data, 'real');
});

test('parseFrame skips unknown fields silently', () => {
  const out = parseFrame('foo: bar\ndata: real\n\n');
  assert.ok(out);
  assert.equal(out.event.data, 'real');
  // The `foo: bar` field is dropped; the event has no `.foo` property.
  assert.equal((out.event as unknown as Record<string, unknown>).foo, undefined);
});

test('parseFrame tolerates CRLF line endings', () => {
  const out = parseFrame('data: hello\r\n\r\n');
  assert.ok(out);
  assert.equal(out.event.data, 'hello');
  assert.equal(out.consumed, 'data: hello\r\n\r\n'.length);
});

test('parseFrame tolerates whitespace-only frames', () => {
  // Browsers tolerate a stray blank frame; the SDK does too.
  const out = parseFrame('\n\n');
  assert.ok(out);
  assert.equal(out.event.event, 'message');
  assert.equal(out.event.data, '');
});

test('parseFrame consumes only the leading frame on a multi-frame buffer', () => {
  // Two frames back-to-back; the iterator carries the remainder.
  const buf = 'data: first\n\ndata: second\n\n';
  const first = parseFrame(buf);
  assert.ok(first);
  assert.equal(first.event.data, 'first');
  assert.equal(first.consumed, 'data: first\n\n'.length);
  const remainder = buf.slice(first.consumed);
  const second = parseFrame(remainder);
  assert.ok(second);
  assert.equal(second.event.data, 'second');
});

test('parseFrame parses a field with no value (per spec)', () => {
  // `data:` with no value is an empty-string data field per §9.2.4.
  const out = parseFrame('data\n\n');
  assert.ok(out);
  assert.equal(out.event.data, '');
});

test('parseFrame handles value containing a colon (only first colon is separator)', () => {
  // Per spec, the value starts AFTER the first colon, optionally
  // skipping a single leading space.
  const out = parseFrame('data: foo:bar\n\n');
  assert.ok(out);
  assert.equal(out.event.data, 'foo:bar');
});

test('parseFrame drops a single leading space from the value', () => {
  // `data: hello` parses as `hello` (the leading space is stripped).
  const out = parseFrame('data: hello\n\n');
  assert.ok(out);
  assert.equal(out.event.data, 'hello');
});

test('parseFrame keeps the space when there are two (per spec, only one is stripped)', () => {
  // `data:  hello` parses as ` hello` (one space stripped, one kept).
  const out = parseFrame('data:  hello\n\n');
  assert.ok(out);
  assert.equal(out.event.data, ' hello');
});

test('parseFrame drops non-finite retry values', () => {
  // `retry: not-a-number` becomes Number('not-a-number') = NaN.
  // The SDK must not propagate NaN — callers expect `retry?: number`
  // to be a usable ms hint.
  const out = parseFrame('retry: not-a-number\ndata: x\n\n');
  assert.ok(out);
  assert.equal(out.event.retry, undefined);
});
