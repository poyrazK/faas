"""Function template for faas (python312).

Functions are invoked directly by the runner — no HTTP server. The
CLI forces `--runtime python312 --handler handler.handler` when
deploying this template.

`handler(event, ctx)` returns a dict with `statusCode`, optional
`headers`, and a string `body`. The runner ships the response back to
the gateway which forwards it to the customer.
"""
import json


async def handler(event, ctx):
    ctx.log.info("function invoked", extra={"invocation_id": ctx.invocation_id})
    return {
        "statusCode": 200,
        "headers": {"content-type": "application/json"},
        "body": json.dumps(
            {
                "ok": True,
                "invocation_id": ctx.invocation_id,
                "received": event,
            }
        ),
    }
