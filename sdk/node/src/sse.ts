// src/sse.ts — hand-rolled SSE (Server-Sent Events) wrapper.
//
// Mirrors `pkg/api/sse.go` and `pkg/api/logs.go` byte-for-byte on the
// wire-format contract:
//   * Each event is zero or more `field: value` lines followed by a
//     blank line.
//   * `data:` fields within an event are joined with `\n` (per the
//     HTML5 SSE spec).
//   * Lines starting with `:` are comments and ignored.
//   * Unknown fields are surfaced verbatim on the event object.
//
// The generator does NOT emit an SSE client (the apid daemon's SSE
// endpoints are out of scope for the OpenAPI surface), so this file
// ships in PR 5 even though no smoke test exercises it. The
// generator-emitted services stay JSON-only.

/** A single SSE frame as parsed off the wire. */
export interface SseEvent {
  /** The `event:` field, lowercased; absent → 'message' per the SSE
   *  default (HTML5 §9.2.4). */
  event: string;
  /** Concatenated `data:` field values joined with `\n`. */
  data: string;
  /** `id:` field, if present. The HTML5 spec requires the client to
   *  send it back as `Last-Event-ID` on reconnect; we surface it
   *  for callers that want to implement their own resume protocol. */
  id?: string;
  /** Retry hint (ms). */
  retry?: number;
}

/**
 * Parse a single SSE frame from a buffered string. The buffer is
 * expected to start at a frame boundary and end after the
 * terminating `\n\n`. Returns the event and the number of bytes
 * consumed. Empty / whitespace-only buffers return `null` and
 * consume the whitespace.
 *
 * Exported for tests; the public API is `streamSse`.
 */
export function parseFrame(buffer: string): { event: SseEvent; consumed: number } | null {
  if (buffer.length === 0) return null;
  // Find the first frame terminator. Per HTML5 §9.2.4, the
  // terminator is one blank line; the SDK accepts LF, CRLF, or
  // CR as the line separator. We search for the canonical 2-byte
  // markers and pick the leftmost.
  const terminators = ['\n\n', '\r\n\r\n', '\r\r'];
  let term = -1;
  let termLen = 0;
  for (const t of terminators) {
    const idx = buffer.indexOf(t);
    if (idx !== -1 && (term === -1 || idx < term)) {
      term = idx;
      termLen = t.length;
    }
  }
  if (term === -1) {
    // No terminator yet; we need more bytes.
    return null;
  }
  const raw = buffer.slice(0, term);
  const consumed = term + termLen;

  // Skip pure whitespace frames (browser tolerates this; we do too).
  if (raw.trim() === '') {
    return { event: { event: 'message', data: '' }, consumed };
  }

  let eventName = 'message';
  const dataParts: string[] = [];
  let id: string | undefined;
  let retry: number | undefined;

  // Split on LF; tolerate CRLF by trimming \r.
  const lines = raw.split('\n');
  for (const line of lines) {
    const stripped = line.endsWith('\r') ? line.slice(0, -1) : line;
    if (stripped === '' || stripped.startsWith(':')) continue;
    const colon = stripped.indexOf(':');
    let field: string;
    let value: string;
    if (colon === -1) {
      field = stripped;
      value = '';
    } else {
      field = stripped.slice(0, colon);
      // Per spec, the value starts AFTER the first space (or at the
      // colon if no space).
      value = stripped[colon + 1] === ' '
        ? stripped.slice(colon + 2)
        : stripped.slice(colon + 1);
    }
    switch (field) {
      case 'event':
        eventName = value;
        break;
      case 'data':
        dataParts.push(value);
        break;
      case 'id':
        id = value;
        break;
      case 'retry':
        retry = Number(value);
        break;
      default:
        // Unknown fields: ignored. The spec lets servers invent
        // private ones; the SDK does not propagate them.
        break;
    }
  }

  const event: SseEvent = { event: eventName, data: dataParts.join('\n') };
  if (id !== undefined) event.id = id;
  if (retry !== undefined && Number.isFinite(retry)) event.retry = retry;
  return { event, consumed };
}

/**
 * Stream SSE events from a `Response` whose body is a text/event-stream.
 * Yields one `SseEvent` per frame. Honours `AbortSignal` for both
 * reading and the caller's cancellation contract (the daemon's
 * `/v1/logs/{app_id}/tail` endpoint closes the body on cancel).
 *
 * The generator emits `CancelablePromise<T>` for JSON endpoints; SSE
 * endpoints are not in the OpenAPI spec, so this helper is the
 * only way to consume them from the SDK today.
 */
export async function* streamSse(
  resp: Response,
  signal?: AbortSignal,
): AsyncGenerator<SseEvent, void, void> {
  if (!resp.body) {
    throw new Error('streamSse: response has no body');
  }
  const reader = resp.body.getReader();
  const decoder = new TextDecoder('utf-8');
  let buffer = '';
  try {
    while (true) {
      if (signal?.aborted) {
        return;
      }
      const { value, done } = await reader.read();
      if (done) return;
      buffer += decoder.decode(value, { stream: true });
      // Drain complete frames from the buffer. A chunk boundary can
      // split a frame, so we carry the trailing partial frame across
      // reads (same role as Go's bufio.Scanner in pkg/api/sse.go).
      let frame: { event: SseEvent; consumed: number } | null;
      // eslint-disable-next-line no-cond-assign
      while ((frame = parseFrame(buffer)) !== null) {
        buffer = buffer.slice(frame.consumed);
        yield frame.event;
      }
    }
  } finally {
    // Always release the reader; cancelled calls must not leak.
    try { reader.releaseLock(); } catch { /* already released */ }
  }
}