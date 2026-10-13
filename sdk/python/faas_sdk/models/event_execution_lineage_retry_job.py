from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_execution_lineage_retry_job_status import (
    EventExecutionLineageRetryJobStatus,
    check_event_execution_lineage_retry_job_status,
)

if TYPE_CHECKING:
    from ..models.event_execution_lineage_retry_job_item import EventExecutionLineageRetryJobItem


T = TypeVar("T", bound="EventExecutionLineageRetryJob")


@_attrs_define
class EventExecutionLineageRetryJob:
    request_id: UUID
    subscription_id: UUID
    app_id: UUID
    status: EventExecutionLineageRetryJobStatus
    processed: int
    """Cases evaluated or skipped before cancellation; cancelled items are not counted."""
    items: list[EventExecutionLineageRetryJobItem]
    created_at: datetime.datetime
    updated_at: datetime.datetime
    retain_until: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        subscription_id = str(self.subscription_id)

        app_id = str(self.app_id)

        status: str = self.status

        processed = self.processed

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        retain_until = self.retain_until.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "request_id": request_id,
                "subscription_id": subscription_id,
                "app_id": app_id,
                "status": status,
                "processed": processed,
                "items": items,
                "created_at": created_at,
                "updated_at": updated_at,
                "retain_until": retain_until,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_execution_lineage_retry_job_item import EventExecutionLineageRetryJobItem

        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        subscription_id = UUID(d.pop("subscription_id"))

        app_id = UUID(d.pop("app_id"))

        status = check_event_execution_lineage_retry_job_status(d.pop("status"))

        processed = d.pop("processed")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = EventExecutionLineageRetryJobItem.from_dict(items_item_data)

            items.append(items_item)

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        retain_until = datetime.datetime.fromisoformat(d.pop("retain_until"))

        event_execution_lineage_retry_job = cls(
            request_id=request_id,
            subscription_id=subscription_id,
            app_id=app_id,
            status=status,
            processed=processed,
            items=items,
            created_at=created_at,
            updated_at=updated_at,
            retain_until=retain_until,
        )

        return event_execution_lineage_retry_job
