from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_execution_lineage_case_backfill_status import (
    EventExecutionLineageCaseBackfillStatus,
    check_event_execution_lineage_case_backfill_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventExecutionLineageCaseBackfill")


@_attrs_define
class EventExecutionLineageCaseBackfill:
    """Retained diagnostic scan status, durable cursor, and repair counters for one subscription."""

    subscription_id: UUID
    app_id: UUID
    status: EventExecutionLineageCaseBackfillStatus
    through_outbox_id: int
    cursor: int
    scanned: int
    recorded: int
    existing: int
    associated: int
    unresolved: int
    conflicts: int
    request_id: UUID | Unset = UNSET
    previous_request_id: UUID | Unset = UNSET
    created_at: datetime.datetime | Unset = UNSET
    updated_at: datetime.datetime | Unset = UNSET
    retain_until: datetime.datetime | Unset = UNSET
    """Work deadline; latest scan metadata remains available while its coverage row exists."""

    def to_dict(self) -> dict[str, Any]:
        subscription_id = str(self.subscription_id)

        app_id = str(self.app_id)

        status: str = self.status

        through_outbox_id = self.through_outbox_id

        cursor = self.cursor

        scanned = self.scanned

        recorded = self.recorded

        existing = self.existing

        associated = self.associated

        unresolved = self.unresolved

        conflicts = self.conflicts

        request_id: str | Unset = UNSET
        if not isinstance(self.request_id, Unset):
            request_id = str(self.request_id)

        previous_request_id: str | Unset = UNSET
        if not isinstance(self.previous_request_id, Unset):
            previous_request_id = str(self.previous_request_id)

        created_at: str | Unset = UNSET
        if not isinstance(self.created_at, Unset):
            created_at = self.created_at.isoformat()

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        retain_until: str | Unset = UNSET
        if not isinstance(self.retain_until, Unset):
            retain_until = self.retain_until.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subscription_id": subscription_id,
                "app_id": app_id,
                "status": status,
                "through_outbox_id": through_outbox_id,
                "cursor": cursor,
                "scanned": scanned,
                "recorded": recorded,
                "existing": existing,
                "associated": associated,
                "unresolved": unresolved,
                "conflicts": conflicts,
            }
        )
        if request_id is not UNSET:
            field_dict["request_id"] = request_id
        if previous_request_id is not UNSET:
            field_dict["previous_request_id"] = previous_request_id
        if created_at is not UNSET:
            field_dict["created_at"] = created_at
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at
        if retain_until is not UNSET:
            field_dict["retain_until"] = retain_until

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        subscription_id = UUID(d.pop("subscription_id"))

        app_id = UUID(d.pop("app_id"))

        status = check_event_execution_lineage_case_backfill_status(d.pop("status"))

        through_outbox_id = d.pop("through_outbox_id")

        cursor = d.pop("cursor")

        scanned = d.pop("scanned")

        recorded = d.pop("recorded")

        existing = d.pop("existing")

        associated = d.pop("associated")

        unresolved = d.pop("unresolved")

        conflicts = d.pop("conflicts")

        _request_id = d.pop("request_id", UNSET)
        request_id: UUID | Unset
        if isinstance(_request_id, Unset):
            request_id = UNSET
        else:
            request_id = UUID(_request_id)

        _previous_request_id = d.pop("previous_request_id", UNSET)
        previous_request_id: UUID | Unset
        if isinstance(_previous_request_id, Unset):
            previous_request_id = UNSET
        else:
            previous_request_id = UUID(_previous_request_id)

        _created_at = d.pop("created_at", UNSET)
        created_at: datetime.datetime | Unset
        if isinstance(_created_at, Unset):
            created_at = UNSET
        else:
            created_at = datetime.datetime.fromisoformat(_created_at)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        _retain_until = d.pop("retain_until", UNSET)
        retain_until: datetime.datetime | Unset
        if isinstance(_retain_until, Unset):
            retain_until = UNSET
        else:
            retain_until = datetime.datetime.fromisoformat(_retain_until)

        event_execution_lineage_case_backfill = cls(
            subscription_id=subscription_id,
            app_id=app_id,
            status=status,
            through_outbox_id=through_outbox_id,
            cursor=cursor,
            scanned=scanned,
            recorded=recorded,
            existing=existing,
            associated=associated,
            unresolved=unresolved,
            conflicts=conflicts,
            request_id=request_id,
            previous_request_id=previous_request_id,
            created_at=created_at,
            updated_at=updated_at,
            retain_until=retain_until,
        )

        return event_execution_lineage_case_backfill
