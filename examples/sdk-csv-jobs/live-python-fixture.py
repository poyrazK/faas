"""Local integration driver: production decoder on the loopback WS fixture.

Products use RealtimeWebSocketClient (wss) as documented in sdk-live-jobs.md.
This test-only adapter permits exactly the local fixture's ws://127.0.0.1 URL.
"""

import asyncio
import json
import os
from contextlib import aclosing
from urllib.parse import urlsplit

import httpx
from faas_sdk.preview import LiveJob, realtime_live_job_source
from faas_sdk.preview.realtime_websocket import RealtimeWebSocketSession
from websockets.asyncio.client import connect


async def main():
    resource = os.environ["GREGALE_TEST_IMPORT"]
    async with httpx.AsyncClient(
        base_url=os.environ["GREGALE_TEST_LIVE_APP"],
        headers={"Authorization": "Bearer " + os.environ["GREGALE_TEST_APP_TOKEN"]},
    ) as app:

        async def get(action):
            response = await app.get(f"/files/{resource}/{action}")
            response.raise_for_status()
            return response.json()

        async def native():
            ticket = await get("live-ticket")
            url = urlsplit(ticket["url"])
            assert url.scheme == "ws" and url.hostname == "127.0.0.1"
            assert ticket["channel"] == "import:" + resource
            socket = await connect(
                ticket["url"],
                additional_headers={"Authorization": "Bearer " + ticket["ticket"]},
                subprotocols=["gregale.realtime.v2"],
                proxy=None,
            )
            return RealtimeWebSocketSession(socket, consumer=None, buffer_size=32)

        async def snapshot():
            return await get("snapshot")

        live = LiveJob(
            resource_id=resource,
            load_snapshot=snapshot,
            timeout=60,
            source=realtime_live_job_source(
                connect=native, channel="import:" + resource
            ),
        )
        seen = []
        async with aclosing(live.updates()) as updates:
            async for state in updates:
                seen.append(state.status)
                assert state.status != "failed"
        assert state.report == "ready"
        print(json.dumps(seen))


asyncio.run(main())
