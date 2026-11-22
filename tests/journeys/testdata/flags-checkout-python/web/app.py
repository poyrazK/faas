"""The Python checkout probe of the Flags SDK journey (tests/journeys,
TestFlagsSDKs/python). It uses the repository's Python flags SDK source,
which the journey copies in as gregale_flags.py, and the API URL the journey
writes to config.json. The same contract as the Node probe (flags-checkout):

  GET /                    {"probe", "customer"}: the verified customer
  GET /debug               why the SDK has no configuration: the refresh
                           error and the identity and runtime statuses
  GET /checkout            checks the "checkout" variant flag, marks it used
  GET /checkout?convert=1  also tracks an order_completed conversion

GregaleFlagsMiddleware pins one configuration per request and returns the
request's flag evidence, so the gateway records the exposure.
"""
import json
import os
import pathlib
from urllib.parse import parse_qs

import httpx

from gregale_flags import _FLAG_CAPABILITIES as FLAG_CAPABILITIES  # the SDK exports no public name
from gregale_flags import GregaleFlags, GregaleFlagsMiddleware

CONFIG = json.loads((pathlib.Path(__file__).parent / "config.json").read_text())
flags = GregaleFlags(CONFIG["apiURL"], refresh_ms=2000, max_stale_ms=10000)


async def respond(send, body):
    raw = json.dumps(body).encode()
    await send({"type": "http.response.start", "status": 200,
                "headers": [(b"content-type", b"application/json"), (b"content-length", str(len(raw)).encode())]})
    await send({"type": "http.response.body", "body": raw})


async def debug():
    """What the SDK's refresh sees, without the token."""
    out = {"identity_endpoint": bool(os.environ.get("FAAS_WORKLOAD_IDENTITY_ENDPOINT"))}
    try:
        await flags.refresh()
        out["refresh"] = "ok"
    except Exception as err:  # noqa: BLE001 - reported to the journey
        out["refresh"] = str(err)
    try:
        async with httpx.AsyncClient(timeout=5) as client:
            ir = await client.get(os.environ.get("FAAS_WORKLOAD_IDENTITY_ENDPOINT", "http://127.0.0.1:1/"),
                                  params={"audience": "gregale:flags"})
            out["identity_status"] = ir.status_code
            if ir.status_code != 200:
                out["identity_body"] = ir.text[:300]
            else:
                fr = await client.get(CONFIG["apiURL"].rstrip("/") + "/v1/runtime/flags", headers={
                    "Authorization": "Bearer " + ir.json()["access_token"],
                    "X-Faas-Flags-Capabilities": FLAG_CAPABILITIES,
                })
                out["runtime_status"] = fr.status_code
                out["runtime_body"] = fr.text[:400]
    except Exception as err:  # noqa: BLE001
        out["probe_error"] = str(err)
    return out


async def checkout(scope, receive, send):
    headers = {k.decode("latin-1").lower(): v.decode("latin-1") for k, v in scope["headers"]}
    customer = headers.get("x-faas-platform-tenant-id", "")
    path = scope["path"]
    if path == "/debug":
        await respond(send, await debug())
        return
    if path != "/checkout":
        await respond(send, {"probe": "gregale-e2e-flags", "customer": customer})
        return
    convert = parse_qs(scope.get("query_string", b"").decode()).get("convert") == ["1"]
    decision = flags.variant("checkout", "fallback")
    flags.used("checkout")
    tracked = flags.track("order_completed", 1200) if convert else False
    await flags.flush()
    await respond(send, {
        "probe": "gregale-e2e-flags",
        "customer": customer,
        "variant": decision.value,
        "reason": decision.reason,
        "rule_id": decision.rule_id or "",
        "config_version": decision.config_version,
        "tracked": tracked,
        "dropped": flags.dropped_events(),
    })


inner = GregaleFlagsMiddleware(checkout, flags)


async def app(scope, receive, send):
    if scope["type"] == "lifespan":
        while True:
            message = await receive()
            if message["type"] == "lifespan.startup":
                await flags.start()
                await send({"type": "lifespan.startup.complete"})
            elif message["type"] == "lifespan.shutdown":
                await flags.close()
                await send({"type": "lifespan.shutdown.complete"})
                return
    await inner(scope, receive, send)
