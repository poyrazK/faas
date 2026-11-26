"""Local acceptance driver. The private request file is a fixture, not an HTTP auth boundary."""

from __future__ import annotations

import asyncio
import hmac
import json
import os
import re
import sys
from pathlib import Path
from typing import Any

import psycopg
from event_pipeline import ImportRecord, csv_event_pipeline
from faas_sdk.preview import (
    QueryToolError,
    QueryMCPError,
    CompensationError,
    EventOutboxError,
    EventPipelineDelivery,
    EventPipelineError,
    EventPipelineOutcome,
    OutboundActionError,
    PipelineScope,
    PostgresEventPipelineStore,
    StripeWebhookError,
)
from faas_sdk.preview.event_consumer_transactions import (
    EventConsumerRequest,
    event_consumer_request_from_headers,
)
from psycopg.rows import dict_row


async def execute(request: dict[str, Any]) -> dict[str, Any] | None:
    if request["action"].startswith("query-"):
        from query_driver import execute as query_execute
        return await query_execute(request)
    if request["action"].startswith("refund-"):
        from refund_driver import execute as refund_execute
        return await refund_execute(request)
    if request["action"].startswith("compensation-"):
        from compensation_driver import execute as compensation_execute
        return await compensation_execute(request)
    if request["action"].startswith("tool-"):
        from tool_driver import execute as tool_execute
        return await tool_execute(request)
    if request["action"].startswith("outbox-"):
        from outbox_driver import execute as outbox_execute
        return await outbox_execute(request)
    if request["action"].startswith("stripe-"):
        from stripe_driver import execute as stripe_execute
        return await stripe_execute(request)
    scope = PipelineScope.from_dict(json.loads(os.environ["GREGALE_PIPELINE_SCOPE"]))
    async with await psycopg.AsyncConnection.connect(
        os.environ["DATABASE_URL"],
        autocommit=True,
        row_factory=dict_row,
        options="-c client_encoding=UTF8 -c statement_timeout=5000",
    ) as connection:

        async def query(sql: str, values: list[Any]) -> list[dict[str, Any]]:
            parameters = []

            def parameter(match: re.Match[str]) -> str:
                parameters.append(values[int(match[1]) - 1])
                return "%s"

            sql = re.sub(r"\$(\d+)", parameter, sql)
            cursor = await connection.execute(sql, parameters)
            return list(await cursor.fetchall())

        async def load(import_id: str) -> ImportRecord | None:
            cursor = await connection.execute(
                "SELECT owner_id,input FROM sdk_csv_event_inputs WHERE import_id=%s",
                [import_id],
            )
            row = await cursor.fetchone()
            return (
                ImportRecord(owner_id=row["owner_id"], input=row["input"])
                if row
                else None
            )

        async def verify(delivery: EventPipelineDelivery) -> EventConsumerRequest:
            supplied = delivery.headers.get("authorization", "")
            if not isinstance(supplied, str) or not hmac.compare_digest(
                supplied, "Bearer " + os.environ["GREGALE_CSV_EVENT_KEY"]
            ):
                raise ValueError("unauthorized")
            return event_consumer_request_from_headers(
                delivery.headers, delivery.body, scope.account_id, scope.app_id
            )

        async def authorize(owner: str, access: str, action: str) -> bool:
            # The fixture's caller has already authenticated this principal.
            return owner == access and access in ("alice", "bob")

        pipeline = csv_event_pipeline(
            scope=scope,
            subscription_id=request["subscription_id"],
            source=request["source"],
            store=PostgresEventPipelineStore(query),
            token=os.environ["FAAS_TOKEN"],
            verify=verify,
            load_import=load,
            authorize_owner=authorize,
        )
        delivery = EventPipelineDelivery(
            headers=request["delivery"]["headers"],
            body=request["delivery"]["body"].encode(),
        )
        result: EventPipelineOutcome | None
        if request["action"] == "reconcile":
            result = await pipeline.reconcile(
                delivery, request["run_id"], request["access"], timeout=10
            )
        elif request["action"] == "inspect":
            result = await pipeline.inspect(delivery, request["access"], timeout=10)
        else:
            result = await pipeline.handle(delivery, request["access"], timeout=10)
        return (
            None
            if result is None
            else {
                "state": result.state,
                "replayed": result.replayed,
                "receipt": result.receipt.to_dict(),
            }
        )


async def main() -> None:
    if sys.argv[1] != "--stdio":
        print(json.dumps(await execute(json.loads(Path(sys.argv[1]).read_text()))))
        return
    # A private fixture pipe avoids repeating thousands of generated SDK imports.
    # Each message still constructs a new adapter and opens a fresh connection.
    print(json.dumps({"ready": True}), flush=True)
    while line := await asyncio.to_thread(sys.stdin.readline):
        request = json.loads(line)
        try:
            result = await execute(request)
        except (EventPipelineError, EventOutboxError, StripeWebhookError, CompensationError, OutboundActionError, QueryToolError, QueryMCPError) as error:
            result = {"code": error.code}
        print(
            json.dumps({"request_id": request["request_id"], "result": result}),
            flush=True,
        )


if __name__ == "__main__":
    try:
        asyncio.run(main())
    except (EventPipelineError, EventOutboxError, StripeWebhookError, CompensationError, OutboundActionError, QueryToolError, QueryMCPError) as error:
        print(json.dumps({"code": error.code}))
        raise SystemExit(2) from None
