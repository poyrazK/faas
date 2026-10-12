# ADR-999: Streamed function responses and multi-value headers

- **Status:** accepted
- **Date:** 2026-10-12
- **Related:** spec §4.9 (function runners), ADR-078 (waitUntil tail),
  ADR-102 (gateway response streaming), ADR-987 (binary runtime)

## Context

Spec §4.9 said "Streaming, websockets: not in v1 for functions". A
function's response crossed the runner as one JSON line holding the whole
body, so a function could not send server-sent events, stream model
tokens or produce a large download, and the runner held every body in
memory. The gateway already streams responses for apps that enable it
(ADR-102); functions never produced a stream for it to pass on.

The same line carried one string per header. The runners kept only the
first value of a repeated request header, and the Go SDK sent only the
first `Set-Cookie`, which breaks login flows that set several cookies.

## Decision

1. **Response frames.** A handler's response is either one line, a
   complete §4.9 response as before, or a sequence of lines carrying a
   `frame` field:
   - `{"frame":"head","status":200,"headers":{…}}` sends the status and
     headers;
   - `{"frame":"chunk","body_b64":"…"}` sends body bytes;
   - `{"frame":"end"}` closes the response;
   - `{"frame":"error","message":"…"}` aborts it.

   The runner writes the head as soon as it arrives and flushes every
   chunk. Once the status is sent, an error frame or a broken handler can
   only abort the response, so the client sees a truncated body rather
   than a clean end. A worker is reused only after a clean `end`. The
   invocation deadline (30 s) covers the whole stream. The waitUntil
   drain runs after the stream ends.

2. **Multi-value headers.** A response header value may be a string or a
   list of strings, one header line per element, in complete responses
   and head frames alike. A repeated request header is joined into one
   string, with `", "` (RFC 9110 §5.3) or `"; "` for `Cookie` (RFC 6265
   §5.4), instead of keeping only the first value.

3. **One runner core.** The five runners (node22, node24, python312,
   python313, go124) were near-copies of the same request loop. They now
   call `guest/runners/internal.Serve`, which owns the envelope, the
   worker-pool and one-shot paths, the frames, the tail drain and the
   guest evidence. The worker pool gains `OpenIfSupported`, which keeps a
   worker for the length of a streamed response.

4. **When a function streams.** Streaming is opt-in by response shape, so
   existing handlers keep their exact behavior:
   - **Node Fetch API:** a `Response` body that is complete within one
     turn of the event loop is sent as one response, as before; a body
     still being produced (a `ReadableStream` fed over time) streams.
   - **Node event handler:** a `body` that is an async iterable (async
     generator, Node stream) streams.
   - **Python:** a `body` that is a generator, async generator or other
     iterator streams; strings, bytes, dicts and lists do not.
   - **Go SDK (`function.Serve`), `binary` and custom handlers:** the
     first `Flush` sends the head and each later `Flush` sends what was
     written since. A handler that never flushes sends one response.

5. **Gateway.** The gateway's existing ADR-102 streaming path carries the
   frames to the client: on for Hobby, Pro and Scale by default, buffered
   on Free and for apps with `streaming_enabled` off. No gateway change is
   needed.

## Consequences

- Functions can serve server-sent events, token streams and large bodies
  without holding them in memory. Free functions still answer correctly,
  buffered at the gateway.
- Handlers built before this change are unaffected: a complete one-line
  response is unchanged, and the runner reads both shapes. New adapters
  ship with the next deploy of each function, as runner changes always
  have.
- A handler that sets several cookies now sends all of them, and a
  handler reading a repeated request header sees every value.
- Websockets for functions remain out of scope.

## Alternatives

- **A separate streaming protocol marker.** Rejected: the frame field
  already distinguishes the shapes per response, so one worker can answer
  both.
- **Always streaming Fetch responses.** Rejected: it would drop
  `Content-Length` and turn mid-body errors into truncated responses for
  every existing Fetch handler.
- **Per-runtime streaming code in each runner.** Rejected in favor of one
  shared core; the five copies had already drifted in comments and docs.
