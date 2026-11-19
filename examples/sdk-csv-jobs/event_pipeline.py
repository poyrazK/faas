"""A business event starts the existing reviewed CSV pipeline."""

from __future__ import annotations

from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import Any

from event_generated import Input as EventInput
from event_generated import contract as event_contract
from faas_sdk.preview import (
    EventPipeline,
    EventPipelineAction,
    EventPipelineDelivery,
    EventPipelineStore,
    PipelineScope,
)
from faas_sdk.preview.event_consumer_transactions import EventConsumerRequest
from reviewed_pipeline_generated import Input, Output, contract


@dataclass(frozen=True, kw_only=True)
class ImportRecord:
    owner_id: str
    input: Input


def csv_event_pipeline(
    *,
    scope: PipelineScope,
    subscription_id: str,
    source: str,
    store: EventPipelineStore,
    token: str,
    verify: Callable[[EventPipelineDelivery], Awaitable[EventConsumerRequest]],
    load_import: Callable[[str], Awaitable[ImportRecord | None]],
    authorize_owner: Callable[[str, str, EventPipelineAction], Awaitable[bool]],
    **httpx_args: Any,
) -> EventPipeline[EventInput, Input, Output, str]:
    from faas_sdk.preview import PipelineEvent

    async def authorize(
        event: PipelineEvent[EventInput], access: str, action: EventPipelineAction
    ) -> str | None:
        record = await load_import(event.data["import_id"])
        if record is None or not await authorize_owner(record.owner_id, access, action):
            return None
        return record.owner_id

    async def map(event: PipelineEvent[EventInput], owner: str) -> Input:
        record = await load_import(event.data["import_id"])
        if (
            record is None
            or record.owner_id != owner
            or record.input["import_id"] != event.data["import_id"]
        ):
            raise ValueError("import_changed")
        return record.input

    return EventPipeline(
        event_contract,
        name="csv-import-events",
        scope=scope,
        subscription_id=subscription_id,
        source=source,
        contract=contract,
        store=store,
        token=token,
        verify=verify,
        authorize=authorize,
        map=map,
        **httpx_args,
    )
