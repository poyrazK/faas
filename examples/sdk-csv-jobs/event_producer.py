"""Atomically save an authenticated owner's import and its event intent."""

from __future__ import annotations

import json

from event_generated import contract
from faas_sdk.preview import (
    EventOutboxPool,
    EventOutboxStatus,
    EventOutboxTransaction,
    EventPublicationOptions,
    EventPublisher,
    PipelineScope,
    with_event_outbox_transaction,
)
from reviewed_pipeline_generated import Input


async def stage_csv_import(
    pool: EventOutboxPool,
    *,
    scope: PipelineScope,
    import_id: str,
    owner_id: str,
    input: Input,
) -> EventOutboxStatus:
    if input["import_id"] != import_id or input["source"]["owner_id"] != owner_id:
        raise ValueError("invalid_import_owner")
    publisher = EventPublisher(contract, scope=scope)

    async def stage(tx: EventOutboxTransaction) -> EventOutboxStatus:
        await tx.execute(
            "INSERT INTO sdk_csv_event_inputs VALUES (%s,%s,%s::jsonb)",
            [import_id, owner_id, json.dumps(input)],
        )
        return await publisher.stage(
            tx,
            {"import_id": import_id},
            EventPublicationOptions(key="csv-import:" + import_id),
        )

    return await with_event_outbox_transaction(pool, stage, timeout=10)
