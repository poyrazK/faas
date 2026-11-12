# `python313` runtime

Python 3.13 for functions. `python312` stays the default for new Python
functions; choose `python313` as the app's runtime to use it. Existing
`python312` apps are unaffected. Builds use Railpack's Python plan.

## Function contract

The [handler API](../handler-api.md) lists everything a handler receives
and can return.

Your source is a `handler.py` exporting a callable. It is loaded once, in a long-lived process that serves request
after request, so module-level state such as database clients is reused
between requests. Handlers written for the older stdin/stdout protocol still
run one process per request. The contract is identical to `python312`; only the runtime id differs.

The handler filename **stays version-neutral**: `/app/handler.py` for
both `python312` and `python313`. The version is bound by the OCI
base image, not by the handler filename, so you can move
existing `python312` handlers to `python313` by setting the runtime
field without renaming files.

Gregale sets `FAAS_RUNTIME=python313` in the handler's
environment so your code can branch on the runtime.

### Streaming

A handler streams by returning a generator, async generator or other
iterator as its `body`; strings, bytes, dicts and lists are sent
as one response, as before:

```python
import asyncio

def handler(event, ctx):
    async def ticks():
        for i in range(3):
            yield f"data: tick {i}\n\n"
            await asyncio.sleep(1)
    return {"statusCode": 200, "headers": {"content-type": "text/event-stream"}, "body": ticks()}
```

An exception after streaming starts aborts the response; the client sees
a truncated body.
The whole invocation, the stream included, may run for the longer of the
app's request timeout and 30 s (`gregale app APP --request-timeout SECONDS`,
up to 1 min on Hobby, 5 min on Pro and 15 min on Scale),
and the gateway carries it to the client on Hobby, Pro and Scale (Free
buffers it). A header value may be a list, one header line each, so
several `Set-Cookie` headers are sent.

### Minimal handler

```python
# handler.py
def handler(event, ctx):
    # event: {"method", "path", "headers", "query", "body", "body_b64"}
    # body is parsed JSON when the request was JSON, else a string or None.
    return {
        "status": 200,                                  # or "statusCode"
        "headers": {"content-type": "application/json"},
        "body": {"hello": "world", "path": event["path"]},  # dicts are sent as JSON
    }
```

The runner always calls `handler(event, ctx)` with both arguments. It may
be `async def`. A `str` or `bytes` body is sent as is and any other value
is JSON-encoded; returning a dict without a `"body"` key sends the whole
dict as JSON. `event["headers"]` ignores case on lookup, and
`event["body_b64"]` holds the exact request bytes, for signature checks.

### Testing locally

The handler is a plain function, so a test calls it directly:

```python
# test_handler.py — run with `python -m pytest`
from handler import handler

def test_hello():
    event = {"method": "GET", "path": "/hello", "headers": {}, "query": "",
             "body": None, "body_b64": ""}
    res = handler(event, None)
    assert res["status"] == 200
    assert res["body"] == {"hello": "world", "path": "/hello"}
```

Pass a small stand-in for `ctx` if your handler uses it (for example
`ctx.invocation_id`). To send a real request through the platform, use
`gregale invoke APP --path /hello`, or `gregale dev` for a live
edit-and-reload loop.

### CGO

CGO is not applicable to Python runtimes.

## App contract

`python313` is a function runtime. To run your own Python HTTP server, deploy it
as an app instead: Gregale builds it from source (see [Builds](../build.md)) or
runs your container image (see [Apps](../apps.md)).

## Base image

Functions run on a Wolfi (glibc) base with Python held at 3.13 and the
conventional `/usr/local/bin/python3` path. Wheels built for the manylinux
2.17 ABI load. The base is shared by every `python313` app, so only your
`site-packages` and handler count toward your plan's app layer limit (see
[Plans and pricing](../plans.md)).

## Detection

Gregale checks a project for a Dockerfile first, then Node, Python and Go. A
Python project is recognized by `requirements.txt` (or `pyproject.toml`). The `requires-python` field does not pick
the version: set the app's runtime to `python312` or `python313`.

## Failure modes

| Symptom | Cause | Fix |
|---|---|---|
| `ModuleNotFoundError: <pkg>` when the handler runs | The handler imports a package that is not in `requirements.txt` | Add it to `requirements.txt`, redeploy |

## See also

- [Handler API](../handler-api.md)
- [Functions and runtimes](../functions.md)
- [Builds and framework detection](../build.md)
