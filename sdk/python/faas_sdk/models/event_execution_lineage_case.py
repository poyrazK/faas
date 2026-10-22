from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_execution_lineage_case_status import (
    EventExecutionLineageCaseStatus,
    check_event_execution_lineage_case_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventExecutionLineageCase")


@_attrs_define
class EventExecutionLineageCase:
    """Retained reason-coded lineage diagnostic with revision and incarnation guards for reevaluation."""

    subscription_id: UUID
    app_id: UUID
    outbox_id: int
    accepted_at: datetime.datetime
    status: EventExecutionLineageCaseStatus
    reason: str
    revision: int
    retry_count: int
    recorded_at: datetime.datetime
    retain_until: datetime.datetime
    diagnostic_id: UUID | Unset = UNSET
    """Unique incarnation of a diagnostically created case; required in its retry guards."""
    root_invocation_id: UUID | Unset = UNSET
    root_created_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        subscription_id = str(self.subscription_id)

        app_id = str(self.app_id)

        outbox_id = self.outbox_id

        accepted_at = self.accepted_at.isoformat()

        status: str = self.status

        reason = self.reason

        revision = self.revision

        retry_count = self.retry_count

        recorded_at = self.recorded_at.isoformat()

        retain_until = self.retain_until.isoformat()

        diagnostic_id: str | Unset = UNSET
        if not isinstance(self.diagnostic_id, Unset):
            diagnostic_id = str(self.diagnostic_id)

        root_invocation_id: str | Unset = UNSET
        if not isinstance(self.root_invocation_id, Unset):
            root_invocation_id = str(self.root_invocation_id)

        root_created_at: str | Unset = UNSET
        if not isinstance(self.root_created_at, Unset):
            root_created_at = self.root_created_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subscription_id": subscription_id,
                "app_id": app_id,
                "outbox_id": outbox_id,
                "accepted_at": accepted_at,
                "status": status,
                "reason": reason,
                "revision": revision,
                "retry_count": retry_count,
                "recorded_at": recorded_at,
                "retain_until": retain_until,
            }
        )
        if diagnostic_id is not UNSET:
            field_dict["diagnostic_id"] = diagnostic_id
        if root_invocation_id is not UNSET:
            field_dict["root_invocation_id"] = root_invocation_id
        if root_created_at is not UNSET:
            field_dict["root_created_at"] = root_created_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        subscription_id = UUID(d.pop("subscription_id"))

        app_id = UUID(d.pop("app_id"))

        outbox_id = d.pop("outbox_id")

        accepted_at = datetime.datetime.fromisoformat(d.pop("accepted_at"))

        status = check_event_execution_lineage_case_status(d.pop("status"))

        reason = d.pop("reason")

        revision = d.pop("revision")

        retry_count = d.pop("retry_count")

        recorded_at = datetime.datetime.fromisoformat(d.pop("recorded_at"))

        retain_until = datetime.datetime.fromisoformat(d.pop("retain_until"))

        _diagnostic_id = d.pop("diagnostic_id", UNSET)
        diagnostic_id: UUID | Unset
        if isinstance(_diagnostic_id, Unset):
            diagnostic_id = UNSET
        else:
            diagnostic_id = UUID(_diagnostic_id)

        _root_invocation_id = d.pop("root_invocation_id", UNSET)
        root_invocation_id: UUID | Unset
        if isinstance(_root_invocation_id, Unset):
            root_invocation_id = UNSET
        else:
            root_invocation_id = UUID(_root_invocation_id)

        _root_created_at = d.pop("root_created_at", UNSET)
        root_created_at: datetime.datetime | Unset
        if isinstance(_root_created_at, Unset):
            root_created_at = UNSET
        else:
            root_created_at = datetime.datetime.fromisoformat(_root_created_at)

        event_execution_lineage_case = cls(
            subscription_id=subscription_id,
            app_id=app_id,
            outbox_id=outbox_id,
            accepted_at=accepted_at,
            status=status,
            reason=reason,
            revision=revision,
            retry_count=retry_count,
            recorded_at=recorded_at,
            retain_until=retain_until,
            diagnostic_id=diagnostic_id,
            root_invocation_id=root_invocation_id,
            root_created_at=root_created_at,
        )

        return event_execution_lineage_case
