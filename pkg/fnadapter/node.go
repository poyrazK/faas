package fnadapter

// Node translates the public handler(event, ctx) and Fetch API
// ({ fetch }) contracts to the runner's protocol envelope. It lives in the app
// layer so the runner can remain a deliberately tiny, protocol-only binary
// shared by all Node apps.
var Node = `// FAAS_PERSISTENT_PROTOCOL_V1
(async () => {
const pathModule = await import("node:path");
const urlModule = await import("node:url");
const readlineModule = await import("node:readline");
const path = pathModule.default || pathModule;

// Customer logs belong on stderr; stdout is reserved for newline-framed
// response envelopes consumed by faas-runner.
console.log = console.error;
console.info = console.error;
` + NodeResources + `
const postgresResources = installPostgresLifecycle();
const handlerFile = path.join(path.dirname(process.argv[1]), "handler.js");
const mod = await import(urlModule.pathToFileURL(handlerFile).href);
const defaultExport = mod.default;
const eventHandler = mod.handler || (typeof defaultExport === "function" ? defaultExport : null);
const fetchHandler = mod.fetch || (defaultExport && typeof defaultExport.fetch === "function"
  ? defaultExport.fetch.bind(defaultExport)
  : null);
if (typeof eventHandler !== "function" && typeof fetchHandler !== "function") {
  throw new Error("handler.js must export handler/default or default { fetch }");
}
// A worker serves FAAS_FUNCTION_CONCURRENCY requests at once when the app
// sets function_concurrency (ADR-1342); requests and every response line
// then carry the runner's __faas_id.
const concurrency = process.env.FAAS_PERSISTENT_WORKER === "1"
  ? Math.min(Math.max(Number.parseInt(process.env.FAAS_FUNCTION_CONCURRENCY || "1", 10) || 1, 1), 256)
  : 1;
if (process.env.FAAS_PERSISTENT_WORKER === "1") {
  process.stdout.write(JSON.stringify({ __faas_ready: true, concurrency, completion_version: 1,
    resource_lifecycle_version: postgresResources ? 1 : 0 }) + "\n");
}

// Response frames (ADR-999): one line is a complete response; a "head" line
// starts a streamed one that "chunk" lines continue and "end" closes. out is
// one request's output: its id, whether its stream has started, and whether
// the runner cancelled it.
const output = (id) => {
  const out = { streaming: false, cancelled: false, controller: new AbortController(), background: null };
  out.emit = (frame) => {
    if (out.background && out.background.deadline === undefined &&
        (frame.frame === undefined || frame.frame === "end" || frame.frame === "error")) {
      out.background.deadline = Date.now() + out.background.timeout;
    }
    if (!out.cancelled) process.stdout.write(JSON.stringify(id === undefined ? frame : { __faas_id: id, ...frame }) + "\n");
  };
  return out;
};
` + NodeBackground + `
const toBuffer = (piece) => Buffer.isBuffer(piece) ? piece
  : piece instanceof Uint8Array ? Buffer.from(piece.buffer, piece.byteOffset, piece.byteLength)
  : Buffer.from(String(piece));
// Event handlers read headers as Node does (req.headers["x-api-key"]), but
// the envelope carries Go's canonical names ("X-Api-Key"), so every
// lowercase read was undefined (hunt #10, H10-20). Keys are lowercase and
// lookups ignore case, so canonical-case reads keep working.
function caseInsensitiveHeaders(raw) {
  const lower = {};
  for (const [key, value] of Object.entries(raw || {})) lower[key.toLowerCase()] = value;
  return new Proxy(lower, {
    get: (target, prop) => typeof prop === "string" ? target[prop.toLowerCase()] : target[prop],
    has: (target, prop) => typeof prop === "string" ? prop.toLowerCase() in target : prop in target,
  });
}
const emitWhole = (out, status, headers, bytes) => out.emit({ status, headers, body_b64: bytes.toString("base64") });
const emitHead = (out, status, headers) => { out.streaming = true; out.emit({ frame: "head", status, headers }); };
const emitChunk = (out, bytes) => { if (bytes.length > 0) out.emit({ frame: "chunk", body_b64: bytes.toString("base64") }); };
const isStreamBody = (body) => body !== null && typeof body === "object" && !Buffer.isBuffer(body) &&
  !(body instanceof Uint8Array) && typeof body[Symbol.asyncIterator] === "function";
async function streamIterable(out, status, headers, body) {
  emitHead(out, status, headers);
  for await (const piece of body) {
    if (out.cancelled) return;
    emitChunk(out, toBuffer(piece));
  }
  out.emit({ frame: "end" });
}
// A Fetch API body already complete within one turn of the event loop is
// sent as one response, exactly as before; a body still being produced
// (server-sent events, generated text) is streamed as it arrives.
async function respondFetchBody(out, status, headers, body) {
  if (!body) { emitWhole(out, status, headers, Buffer.alloc(0)); return; }
  const reader = body.getReader();
  let cancelling;
  const abort = () => { cancelling ||= reader.cancel().catch(() => {}); };
  out.controller.signal.addEventListener("abort", abort, { once: true });
  if (out.controller.signal.aborted) abort();
  try {
  const pending = Symbol("pending");
  const buffered = [];
  let read = reader.read();
  for (;;) {
    const next = await Promise.race([read, new Promise((resolve) => setImmediate(resolve, pending))]);
    if (next === pending) break;
    if (next.done) { emitWhole(out, status, headers, Buffer.concat(buffered)); return; }
    buffered.push(toBuffer(next.value));
    read = reader.read();
  }
  emitHead(out, status, headers);
  emitChunk(out, Buffer.concat(buffered));
  for (let next = await read; !next.done; next = await reader.read()) {
    if (out.cancelled) { abort(); return; }
    emitChunk(out, toBuffer(next.value));
  }
  out.emit({ frame: "end" });
  } finally {
    out.controller.signal.removeEventListener("abort", abort);
    if (cancelling) await cancelling;
    reader.releaseLock();
  }
}

// handleOne answers one request: input is the raw line, or the decoded
// request on a shared worker.
// The SDK's request context (release, signed deadline, time left): when the
// handler loaded @gregale/sdk-node, the SDK registers its entry point here and
// its service-call fetch reads the context (ADR-20261102082606 amendment 2).
// Without the SDK the handler runs exactly as before.
const GREGALE_REQUEST_CONTEXT = Symbol.for("gregale.sdk.requestContext.v1");
function inGregaleRequestContext(headers, run) {
  const enter = globalThis[GREGALE_REQUEST_CONTEXT];
  return typeof enter === "function" ? enter(headers, run) : run();
}

async function handleOne(input, out) {
  let invocationID = "";
  try {
  const env = typeof input === "string" ? JSON.parse(input) : input;
  setupBackground(env, out);
  const raw = Buffer.from(env.body_b64 || "", "base64").toString("utf8");
  let body = raw;
  if (raw === "") {
    body = null;
  } else {
    try { body = JSON.parse(raw); } catch (_) {}
  }
  const headers = env.headers || {};
  invocationID = headers["x-faas-invocation-id"] || headers["X-Faas-Invocation-Id"] || "";
  // One line per call, like the Python adapter: console.error(msg, fields)
  // printed a multi-line inspect() of the fields, and every line became its
  // own log record (H10-59).
  const formatLogArg = (arg) => {
    if (typeof arg === "string") return arg;
    if (arg instanceof Error) return arg.stack || String(arg);
    try {
      const text = JSON.stringify(arg);
      if (text !== undefined) return text;
    } catch (_) {}
    return String(arg);
  };
  const log = {};
  for (const level of ["debug", "info", "warn", "error"]) {
    log[level] = (...args) => console.error(args.map(formatLogArg).join(" "));
  }
  const ctx = {
    invocation_id: invocationID,
    lifecycle_hook: env.lifecycle_hook || "",
    signal: out.controller.signal,
    log,
    waitUntil(promise) { registerBackground(out, promise); },
  };
  const invoke = async () => {
  let value;
  if (typeof fetchHandler === "function") {
    const method = env.method || "POST";
    const requestURL = new URL(env.path || "/", "http://faas.local");
    if (env.query) requestURL.search = env.query.startsWith("?") ? env.query : "?" + env.query;
    const requestInit = { method, headers, signal: ctx.signal };
    const requestBody = Buffer.from(env.body_b64 || "", "base64");
    if (method !== "GET" && method !== "HEAD" && requestBody.length > 0) requestInit.body = requestBody;
    const request = new Request(requestURL, requestInit);
    value = await fetchHandler(request, { ...process.env }, ctx);
  } else {
    const event = {
      method: env.method || "POST",
      path: env.path || "/",
      headers: caseInsensitiveHeaders(headers),
      query: env.query || "",
      body,
      // Spec §4.9: the exact request bytes. body is parsed JSON when the
      // payload is JSON, which a signature check cannot use.
      body_b64: env.body_b64 || "",
    };
    value = await eventHandler(event, ctx);
  }
  return value;
  };
  if (env.resource_lifecycle === true) {
    if (!postgresResources) throw new Error("postgres_lifecycle_unavailable");
    await postgresResources.handle(env, ctx, invoke);
    emitWhole(out, 204, {}, Buffer.alloc(0));
    return;
  }
  postgresResources?.assertReady();
  // The handler, and any body it streams, run in the SDK's request context.
  await inGregaleRequestContext(headers, async () => {
  const value = await invoke();
  if (value && typeof value === "object" && typeof value.arrayBuffer === "function" &&
      typeof value.status === "number" && value.headers) {
    // Every Set-Cookie line survives (ADR-999); other repeated headers are
    // already combined by the Headers object.
    const fetchHeaders = {};
    for (const [key, item] of value.headers.entries()) {
      if (key.toLowerCase() !== "set-cookie") fetchHeaders[key] = item;
    }
    const cookies = typeof value.headers.getSetCookie === "function" ? value.headers.getSetCookie() : [];
    if (cookies.length > 0) fetchHeaders["set-cookie"] = cookies;
    await respondFetchBody(out, value.status, fetchHeaders, value.body);
    return;
  }
  let status = 200;
  let responseHeaders = {};
  let responseBody = value;
  if (value && typeof value === "object" && !Buffer.isBuffer(value)) {
    status = Number(value.statusCode ?? value.status ?? 200);
    responseHeaders = value.headers || {};
    if (Object.prototype.hasOwnProperty.call(value, "body")) responseBody = value.body;
  }
  // A header value may be a list, one line per element (ADR-999).
  const normalizedHeaders = {};
  for (const [key, item] of Object.entries(responseHeaders)) {
    normalizedHeaders[key] = Array.isArray(item) ? item.map(String) : String(item);
  }
  // An async iterable body (async generator, Node stream) is streamed.
  if (isStreamBody(responseBody)) {
    await streamIterable(out, status, normalizedHeaders, responseBody);
    return;
  }
  if (responseBody === undefined || responseBody === null) responseBody = "";
  if (typeof responseBody !== "string" && !Buffer.isBuffer(responseBody)) {
    responseBody = JSON.stringify(responseBody);
  }
  emitWhole(out, status, normalizedHeaders, Buffer.from(responseBody));
  });
  } catch (err) {
    if (out.cancelled) return;
    const rawMessage = err instanceof Error ? err.message : String(err);
    const message = rawMessage.length > 256 ? rawMessage.slice(0, 256) + "…" : rawMessage;
    console.error(err && err.stack ? err.stack : err);
    // The status is already sent once a stream has started; the error
    // frame tells the runner to abort the response.
    if (out.streaming) {
      out.emit({ frame: "error", message });
      return;
    }
    const responseBody = JSON.stringify({
      error: "handler_error",
      message,
      invocation_id: invocationID,
    });
    out.emit({
      status: 500,
      headers: { "content-type": "application/json; charset=utf-8" },
      body_b64: Buffer.from(responseBody).toString("base64"),
    });
  } finally {
    await drainBackground(out);
  }
}

// Cancellation suppresses output and aborts the request's I/O signal. Ack only
// after its handler and stream have actually stopped; the runner retires an
// uncooperative worker instead of handing its occupied slot to another request.
const inflight = new Map();
const lines = readlineModule.createInterface({ input: process.stdin, crlfDelay: Infinity });
for await (const line of lines) {
  if (!line.trim()) continue;
  let request;
  try { request = JSON.parse(line); } catch (_) { await handleOne(line, output(undefined)); continue; }
  if (!request || typeof request !== "object") {
    await handleOne(request, output(undefined));
    continue;
  }
  if (typeof request.__faas_cancel === "string") {
    const id = request.__faas_cancel;
    const out = inflight.get(id);
    if (out) {
      out.cancelled = true;
      out.controller.abort();
      if (!out.cancelling) {
        out.cancelling = true;
        void out.done.then(() => process.stdout.write(JSON.stringify({ __faas_cancelled: id }) + "\n"));
      }
    } else process.stdout.write(JSON.stringify({ __faas_cancelled: id }) + "\n");
    continue;
  }
  if (typeof request.__faas_id !== "string") {
    await handleOne(request, output(undefined));
    continue;
  }
  const id = request.__faas_id;
  const out = output(id);
  inflight.set(id, out);
  out.done = handleOne(request, out).finally(() => inflight.delete(id));
}
})().catch((err) => {
  console.error(err && err.stack ? err.stack : err);
  process.exitCode = 1;
});
`
