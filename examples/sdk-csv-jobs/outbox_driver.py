"""Private local acceptance pipe; callers have already authenticated fixture inputs."""

from __future__ import annotations

import asyncio
import json
import os
import sys
from dataclasses import asdict
from pathlib import Path
from typing import Any

import psycopg
from faas_sdk.preview import (
    EventContract,
    EventOutbox,
    EventOutboxError,
    EventOutboxRecoveryOptions,
    EventOutboxRetryPolicy,
    EventPublicationOptions,
    EventPublisher,
    PipelineScope,
    PsycopgEventOutboxPool,
    with_event_outbox_transaction,
)


async def execute(request: dict[str, Any]) -> dict[str, Any]:
    scope = PipelineScope.from_dict(
        request.get("scope") or json.loads(os.environ["GREGALE_PIPELINE_SCOPE"])
    )

    async def connect():
        return await psycopg.AsyncConnection.connect(
            os.environ["DATABASE_URL"], autocommit=True
        )

    pool = PsycopgEventOutboxPool(connect)
    outbox = EventOutbox(
        pool,
        scope=scope,
        token=request.get("token", os.environ.get("FAAS_TOKEN", "fixture-owner")),
    )
    action = request["action"].removeprefix("outbox-")
    if action == "stage":
        publisher = EventPublisher(EventContract(request["contract"]), scope=scope)

        async def stage(tx):
            await tx.execute(
                "INSERT INTO outbox_business(key) VALUES (%s)",
                [request["options"]["key"]],
            )
            status = await publisher.stage(
                tx,
                request["input"],
                EventPublicationOptions(**request["options"]),
                retry_window=request.get("retry_window", 86400),
                retry_policy=EventOutboxRetryPolicy(**request["retry_policy"])
                if request.get("retry_policy")
                else None,
            )
            if request.get("rollback"):
                raise EventOutboxError("conflict")
            return status

        return asdict(await with_event_outbox_transaction(pool, stage, timeout=10))
    if action == "recover":
        return asdict(
            await outbox.recover_next(
                EventOutboxRecoveryOptions(timeout=10, backoff=0.001)
            )
        )
    if action == "reconcile":
        return asdict(await outbox.reconcile(request["key"], timeout=10))
    if action == "status":
        status = await outbox.status(request["key"], timeout=10)
        return asdict(status) if status is not None else {}
    raise ValueError("unknown_fixture_action")


async def main():
    if sys.argv[1] != "--stdio":
        print(json.dumps(await execute(json.loads(Path(sys.argv[1]).read_text()))))
        return
    print(json.dumps({"ready": True}), flush=True)
    while line := await asyncio.to_thread(sys.stdin.readline):
        request = json.loads(line)
        try:
            result = await execute(request)
        except EventOutboxError as error:
            result = {"code": error.code}
        print(
            json.dumps({"request_id": request["request_id"], "result": result}),
            flush=True,
        )


if __name__ == "__main__":
    asyncio.run(main())
