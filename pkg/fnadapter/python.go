package fnadapter

// Python translates the public handler(event, ctx) contract to the runner's
// protocol envelope.
const Python = `# FAAS_PERSISTENT_PROTOCOL_V1
import asyncio
import base64
import concurrent.futures
import contextlib
import importlib.util
import inspect
import json
import os
import sys
import threading
import traceback

class _Log:
    def info(self, *args, **kwargs):
        # Match the stdlib logging call shape used by the stock templates.
        # Passing extra through to print raises TypeError and suppresses the
        # handler's response; serialize it as structured context instead.
        extra = kwargs.pop("extra", None)
        kwargs.pop("exc_info", None)
        kwargs.pop("stack_info", None)
        message = " ".join(str(arg) for arg in args)
        if isinstance(extra, dict) and extra:
            message += " " + json.dumps(extra, sort_keys=True, default=str)
        print(message, file=sys.stderr, flush=True)
    debug = info
    warning = info
    warn = info
    error = info

# stdout is protocol-bearing. Route ordinary customer prints — including
# module-level prints during import — to stderr before loading customer code.
real_stdout = sys.stdout
sys.stdout = sys.stderr
handler_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), ".faas-handler.py")
spec = importlib.util.spec_from_file_location("faas_handler_impl", handler_path)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
handler = getattr(module, "handler", None)
if not callable(handler): raise RuntimeError("handler.py must define handler(event, ctx)")
# A worker serves FAAS_FUNCTION_CONCURRENCY requests at once when the app
# sets function_concurrency (ADR-1342); requests and every response line then
# carry the runner's __faas_id. An async handler runs each request as a task;
# a sync handler runs each on its own thread.
handler_is_async = inspect.iscoroutinefunction(handler)
concurrency = 1
if os.environ.get("FAAS_PERSISTENT_WORKER") == "1":
    try:
        concurrency = min(max(int(os.environ.get("FAAS_FUNCTION_CONCURRENCY", "1")), 1), 256)
    except ValueError:
        concurrency = 1
if os.environ.get("FAAS_PERSISTENT_WORKER") == "1":
    real_stdout.write(json.dumps({"__faas_ready": True, "concurrency": concurrency}) + "\n")
    real_stdout.flush()

# One event loop for the life of the worker. asyncio.run() per request
# closed the loop after every invocation, so async clients a handler keeps
# between requests (DB pools, HTTP sessions, queues) were bound to a dead
# loop and the next warm request failed with "bound to a different event
# loop" / "Event loop is closed".
loop = asyncio.new_event_loop()
asyncio.set_event_loop(loop)

class _Context:
    log = _Log()
    def __init__(self, invocation, cancelled=None, lifecycle_hook=""):
        self.invocation_id = invocation
        self.cancelled = cancelled if cancelled is not None else threading.Event()
        self.lifecycle_hook = lifecycle_hook

# Response frames (ADR-999): one line is a complete response; a "head" line
# starts a streamed one that "chunk" lines continue and "end" closes.
def _emit(frame):
    real_stdout.write(json.dumps(frame) + "\n")
    real_stdout.flush()

def _to_bytes(piece):
    if isinstance(piece, (bytes, bytearray)):
        return bytes(piece)
    return str(piece).encode("utf-8")

def _emit_chunk(piece, emit=_emit):
    data = _to_bytes(piece)
    if data:
        emit({"frame": "chunk", "body_b64": base64.b64encode(data).decode("ascii")})

async def _drain_async(body, emit=_emit):
    async for piece in body:
        _emit_chunk(piece, emit)

def _is_stream_body(body):
    # A generator, async generator or other iterator streams; str, bytes,
    # dicts and lists are complete bodies.
    if isinstance(body, (str, bytes, bytearray, dict, list, tuple)):
        return False
    return hasattr(body, "__aiter__") or (hasattr(body, "__iter__") and hasattr(body, "__next__"))

class _Headers(dict):
    # Lowercase keys, case-insensitive lookups: the envelope carries Go's
    # canonical names, so headers["x-api-key"] missed (H10-20).
    def __init__(self, raw):
        super().__init__((str(k).lower(), v) for k, v in raw.items())
    def __getitem__(self, key): return super().__getitem__(str(key).lower())
    def __contains__(self, key): return super().__contains__(str(key).lower())
    def get(self, key, default=None): return super().get(str(key).lower(), default)

def _event(env):
    body_b64 = env.get("body_b64", "") or ""
    # Lossy for non-UTF-8 payloads (matching the Node adapter); the exact
    # bytes stay available to the handler as event["body_b64"].
    raw = base64.b64decode(body_b64).decode("utf-8", errors="replace")
    if raw == "":
        body = None
    else:
        try:
            body = json.loads(raw)
        except json.JSONDecodeError:
            body = raw
    headers = env.get("headers") or {}
    invocation_id = headers.get("x-faas-invocation-id", headers.get("X-Faas-Invocation-Id", ""))
    event = {
        "method": env.get("method") or "POST",
        "path": env.get("path") or "/",
        "headers": _Headers(headers),
        "query": env.get("query") or "",
        "body": body,
        "body_b64": body_b64,
    }
    return event, invocation_id

def _shape(result):
    status = 200
    response_headers = {}
    response_body = result
    if isinstance(result, dict):
        status = int(result.get("statusCode", result.get("status", 200)))
        response_headers = result.get("headers") or {}
        if "body" in result: response_body = result["body"]
    # A header value may be a list, one line per element (ADR-999).
    headers_out = {str(k): ([str(x) for x in v] if isinstance(v, (list, tuple)) else str(v))
                   for k, v in response_headers.items()}
    return status, headers_out, response_body

def _whole(status, headers_out, response_body):
    if response_body is None: response_body = ""
    if not isinstance(response_body, (str, bytes, bytearray)):
        response_body = json.dumps(response_body)
    if isinstance(response_body, str): response_body = response_body.encode("utf-8")
    return {
        "status": status,
        "headers": headers_out,
        "body_b64": base64.b64encode(response_body).decode("ascii"),
    }

def _error_message(exc):
    # Python slices str by code point, so this never splits a character.
    detail = str(exc)
    return detail if len(detail) <= 256 else detail[:256] + "\u2026"

def _error_envelope(message, invocation_id):
    error_body = json.dumps({"error": "handler_error", "message": message, "invocation_id": invocation_id})
    return {
        "status": 500,
        "headers": {"content-type": "application/json; charset=utf-8"},
        "body_b64": base64.b64encode(error_body.encode("utf-8")).decode("ascii"),
    }

def _gregale_context(request):
    # The SDK's request context (release, signed deadline, time left) when
    # the handler imported faas_sdk; its transports read it on service calls
    # (ADR-20261102082606 amendment 2). Without the SDK, nothing changes.
    module = sys.modules.get("faas_sdk.release_context")
    enter = getattr(module, "with_gregale_request_context", None)
    headers = request.get("headers") if isinstance(request, dict) else None
    if enter is None or not isinstance(headers, dict):
        return contextlib.nullcontext()
    return enter(headers)

def _serve_lines():
    for line in sys.stdin:
        if not line.strip():
            continue
        invocation_id = ""
        streaming = False
        # One failing request must not end the persistent worker. An uncaught
        # exception used to escape this loop: the worker died, the runner
        # answered a plain-text 500 that the platform treats as a retryable
        # infrastructure failure, and the durable queue re-ran the failing
        # handler. Mirror the Node adapter: a terminal handler_error envelope.
        try:
            request = json.loads(line)
            with _gregale_context(request):
                event, invocation_id = _event(request)
                result = handler(event, _Context(invocation_id, lifecycle_hook=request.get("lifecycle_hook", "")))
                if inspect.isawaitable(result): result = loop.run_until_complete(result)
                status, headers_out, response_body = _shape(result)
                if _is_stream_body(response_body):
                    streaming = True
                    _emit({"frame": "head", "status": status, "headers": headers_out})
                    if hasattr(response_body, "__aiter__"):
                        loop.run_until_complete(_drain_async(response_body))
                    else:
                        for piece in response_body:
                            _emit_chunk(piece)
                    _emit({"frame": "end"})
                    continue
                envelope = _whole(status, headers_out, response_body)
        except Exception as exc:
            traceback.print_exc()
            message = _error_message(exc)
            if streaming:
                # The status is already sent; the error frame tells the
                # runner to abort the response.
                _emit({"frame": "error", "message": message})
                continue
            envelope = _error_envelope(message, invocation_id)
        real_stdout.write(json.dumps(envelope) + "\n")
        real_stdout.flush()

# Handler threads and the event loop share stdout; one line at a time.
_stdout_lock = threading.Lock()

def _emit_tagged(frame):
    line = json.dumps(frame, separators=(",", ":")) + "\n"
    with _stdout_lock:
        real_stdout.write(line)
        real_stdout.flush()

async def _serve_one(request, rid, cancelled):
    # The id goes first so the runner routes the line without decoding it.
    def emit(frame):
        if not cancelled.is_set():
            _emit_tagged({"__faas_id": rid, **frame})
    invocation_id = ""
    streaming = False
    try:
        with _gregale_context(request):
            event, invocation_id = _event(request)
            status, headers_out, response_body = _shape(await handler(event, _Context(invocation_id, cancelled, request.get("lifecycle_hook", ""))))
            if _is_stream_body(response_body):
                streaming = True
                emit({"frame": "head", "status": status, "headers": headers_out})
                if hasattr(response_body, "__aiter__"):
                    await _drain_async(response_body, emit)
                else:
                    for piece in response_body:
                        _emit_chunk(piece, emit)
                emit({"frame": "end"})
                return
            emit(_whole(status, headers_out, response_body))
    except Exception as exc:
        traceback.print_exc()
        message = _error_message(exc)
        emit({"frame": "error", "message": message} if streaming else _error_envelope(message, invocation_id))

async def _await(awaitable, request=None):
    # run_coroutine_threadsafe runs this in the loop thread's context, not the
    # handler thread's, so it re-enters the request's context itself.
    with _gregale_context(request):
        return await awaitable

def _serve_sync(request, rid, cancelled):
    # Runs on a handler thread. Python cannot interrupt a thread, so a
    # cancelled request's frames are dropped and a streamed body stops at
    # its next piece; the request ends when the handler returns.
    def emit(frame):
        if not cancelled.is_set():
            _emit_tagged({"__faas_id": rid, **frame})
    invocation_id = ""
    streaming = False
    try:
        with _gregale_context(request):
            event, invocation_id = _event(request)
            result = handler(event, _Context(invocation_id, cancelled, request.get("lifecycle_hook", "")))
            if inspect.isawaitable(result):
                result = asyncio.run_coroutine_threadsafe(_await(result, request), loop).result()
            status, headers_out, response_body = _shape(result)
            if _is_stream_body(response_body):
                streaming = True
                emit({"frame": "head", "status": status, "headers": headers_out})
                if hasattr(response_body, "__aiter__"):
                    asyncio.run_coroutine_threadsafe(_await(_drain_async(response_body, emit), request), loop).result()
                else:
                    for piece in response_body:
                        if cancelled.is_set():
                            break
                        _emit_chunk(piece, emit)
                emit({"frame": "end"})
                return
            emit(_whole(status, headers_out, response_body))
    except Exception as exc:
        traceback.print_exc()
        message = _error_message(exc)
        emit({"frame": "error", "message": message} if streaming else _error_envelope(message, invocation_id))

_threads = None

def _start_threaded(request, rid, tasks):
    cancelled = threading.Event()
    work = loop.run_in_executor(_threads, _serve_sync, request, rid, cancelled)
    work.add_done_callback(lambda _done: tasks.pop(rid, None))
    return work, cancelled

def _cancel_threaded(rid, entry):
    # Acknowledged only once the handler's thread is free, so the runner
    # never counts a slot a running handler still holds. A handler that
    # never returns misses the acknowledgement deadline and the runner
    # restarts the worker.
    work, cancelled = entry
    cancelled.set()
    work.add_done_callback(lambda _done: _emit_tagged({"__faas_cancelled": rid}))

# Ack cancellation only once async finally blocks or handler threads have
# completed; a running cleanup still owns its worker slot.
async def _serve_shared():
    reader = asyncio.StreamReader(limit=1 << 30)
    await loop.connect_read_pipe(lambda: asyncio.StreamReaderProtocol(reader), sys.stdin)
    tasks = {}
    while True:
        line = await reader.readline()
        if not line:
            return
        if not line.strip():
            continue
        try:
            request = json.loads(line)
        except ValueError:
            traceback.print_exc()
            continue
        cancel = request.get("__faas_cancel")
        if cancel is not None:
            entry = tasks.get(cancel)
            if entry is not None and entry[1].is_set():
                continue
            if entry is not None and not handler_is_async:
                _cancel_threaded(cancel, entry)
                continue
            if entry is not None:
                work, cancelled = entry
                cancelled.set()
                work.cancel()
                work.add_done_callback(lambda _done, rid=cancel: _emit_tagged({"__faas_cancelled": rid}))
            else:
                _emit_tagged({"__faas_cancelled": cancel})
            continue
        rid = str(request.get("__faas_id", ""))
        if handler_is_async:
            cancelled = threading.Event()
            work = loop.create_task(_serve_one(request, rid, cancelled))
            work.add_done_callback(lambda _done, rid=rid: tasks.pop(rid, None))
            tasks[rid] = (work, cancelled)
        else:
            tasks[rid] = _start_threaded(request, rid, tasks)

if concurrency > 1:
    if not handler_is_async:
        _threads = concurrent.futures.ThreadPoolExecutor(max_workers=concurrency, thread_name_prefix="faas-handler")
    loop.run_until_complete(_serve_shared())
else:
    _serve_lines()
`
