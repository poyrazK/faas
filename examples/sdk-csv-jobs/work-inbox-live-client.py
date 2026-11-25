"""Local exercise of the Python watcher using the CSV app's authenticated routes."""

import asyncio
import json
import sys
from contextlib import aclosing

import httpx
from faas_sdk.preview import (
    LiveWorkInboxError,
    RealtimeWebSocketSession,
    realtime_live_work_inbox_source,
    watch_work_inbox,
)
from websockets.asyncio.client import connect


async def watch(http, url, actor):
    async def request(action, timeout=10):
        response = await http.get(
            f"{url}/{action}", headers={"Authorization": f"Bearer {actor}-review"}, timeout=timeout
        )
        if response.status_code in (401, 403, 404):
            raise LiveWorkInboxError("unauthorized")
        response.raise_for_status()
        return response.json()

    async def run():
        first = await request("ticket")
        initial = first

        async def open_session():
            nonlocal initial
            current = initial or await request("ticket")
            initial = None
            if any(current[key] != first[key] for key in ("url", "channel", "target")):
                raise LiveWorkInboxError("unauthorized")
            # The local protocol fixture uses loopback ws://. Production apps
            # use RealtimeWebSocketClient with a wss:// endpoint.
            socket = await connect(
                current["url"],
                additional_headers={"Authorization": "Bearer " + current["ticket"]},
                subprotocols=["gregale.realtime.v2"],
                proxy=None,
                compression=None,
            )
            return RealtimeWebSocketSession(socket, consumer=None, buffer_size=32)

        async def load_snapshot(remaining):
            return await request("snapshot", min(10, remaining))

        async with aclosing(
            watch_work_inbox(
                target=first["target"],
                source=realtime_live_work_inbox_source(connect=open_session, channel=first["channel"]),
                load_snapshot=load_snapshot,
                timeout=300,
                refresh_interval=60,
            )
        ) as snapshots:
            async for snapshot in snapshots:
                print(json.dumps({"actor": actor, "snapshot": snapshot}), flush=True)

    try:
        await run()
    except LiveWorkInboxError as error:
        print(json.dumps({"actor": actor, "error": error.code}), flush=True)


async def main():
    base, alice_import, bob_import = sys.argv[1:]
    async with httpx.AsyncClient(trust_env=False) as http:
        await asyncio.gather(
            watch(http, f"{base}/imports/{alice_import}/work-inbox", "alice"),
            watch(http, f"{base}/imports/{bob_import}/work-inbox", "bob"),
        )


if __name__ == "__main__":
    asyncio.run(main())
