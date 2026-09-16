"""Function template for gregale (python313).

Mirrors cmd/gregale/templates/function-python/handler.py (python312) —
the only difference is the runtime id (python313). The handler
filename is identical (/app/handler.py, version-neutral on the wire);
the underlying Python version is bound by the OCI base image
(images/runner-python313.Dockerfile), not by this file.

Functions are invoked directly by the runner — no HTTP server. The
CLI forces `--runtime python313 --handler handler.handler` when
deploying this template so the wiring is automatic.

`handler(event, ctx)` returns a dict with `statusCode`, optional
`headers`, and a string `body`. The runner ships the response back to
the gateway which forwards it to the customer.
"""
import json
import os


async def handler(event, ctx):
    # Do not log or echo event: it can contain cookies, API keys,
    # authorization headers, and customer payloads.
    ctx.log.info(
        "function invoked",
        extra={"invocation_id": ctx.invocation_id, "runtime": os.environ.get("FAAS_RUNTIME")},
    )
    return {
        "statusCode": 200,
        "headers": {"content-type": "application/json"},
        "body": json.dumps(
            {
                "ok": True,
                "invocation_id": ctx.invocation_id,
                "runtime": os.environ.get("FAAS_RUNTIME"),
            }
        ),
    }
