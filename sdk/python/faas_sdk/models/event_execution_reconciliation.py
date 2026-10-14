from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_execution_reconciliation_status import (
    EventExecutionReconciliationStatus,
    check_event_execution_reconciliation_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventExecutionReconciliation")


@_attrs_define
class EventExecutionReconciliation:
    """Subscription execution-coverage status and durable progress of forward reconciliation and legacy repair."""

    repair_scanned: int
    """Cumulative retained receipt candidates examined by this request."""
    repair_repaired: int
    """Newly associated authoritative roots; existing associations are not counted."""
    repair_unresolved: int
    """Examined candidates with missing or incompatible execution evidence; not a live blocker count."""
    repair_conflicts: int
    """Examined candidates with competing acceptance or incompatible existing lineage; not a live blocker count."""
    subscription_id: UUID
    app_id: UUID
    status: EventExecutionReconciliationStatus
    unattributed_execution_present: bool
    """A retained app invocation lacks authoritative ledger lineage; unrelated app invocations can conservatively
    block reconciliation."""
    requested_at: datetime.datetime | Unset = UNSET
    collection_started_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        repair_scanned = self.repair_scanned

        repair_repaired = self.repair_repaired

        repair_unresolved = self.repair_unresolved

        repair_conflicts = self.repair_conflicts

        subscription_id = str(self.subscription_id)

        app_id = str(self.app_id)

        status: str = self.status

        unattributed_execution_present = self.unattributed_execution_present

        requested_at: str | Unset = UNSET
        if not isinstance(self.requested_at, Unset):
            requested_at = self.requested_at.isoformat()

        collection_started_at: str | Unset = UNSET
        if not isinstance(self.collection_started_at, Unset):
            collection_started_at = self.collection_started_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "repair_scanned": repair_scanned,
                "repair_repaired": repair_repaired,
                "repair_unresolved": repair_unresolved,
                "repair_conflicts": repair_conflicts,
                "subscription_id": subscription_id,
                "app_id": app_id,
                "status": status,
                "unattributed_execution_present": unattributed_execution_present,
            }
        )
        if requested_at is not UNSET:
            field_dict["requested_at"] = requested_at
        if collection_started_at is not UNSET:
            field_dict["collection_started_at"] = collection_started_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        repair_scanned = d.pop("repair_scanned")

        repair_repaired = d.pop("repair_repaired")

        repair_unresolved = d.pop("repair_unresolved")

        repair_conflicts = d.pop("repair_conflicts")

        subscription_id = UUID(d.pop("subscription_id"))

        app_id = UUID(d.pop("app_id"))

        status = check_event_execution_reconciliation_status(d.pop("status"))

        unattributed_execution_present = d.pop("unattributed_execution_present")

        _requested_at = d.pop("requested_at", UNSET)
        requested_at: datetime.datetime | Unset
        if isinstance(_requested_at, Unset):
            requested_at = UNSET
        else:
            requested_at = datetime.datetime.fromisoformat(_requested_at)

        _collection_started_at = d.pop("collection_started_at", UNSET)
        collection_started_at: datetime.datetime | Unset
        if isinstance(_collection_started_at, Unset):
            collection_started_at = UNSET
        else:
            collection_started_at = datetime.datetime.fromisoformat(_collection_started_at)

        event_execution_reconciliation = cls(
            repair_scanned=repair_scanned,
            repair_repaired=repair_repaired,
            repair_unresolved=repair_unresolved,
            repair_conflicts=repair_conflicts,
            subscription_id=subscription_id,
            app_id=app_id,
            status=status,
            unattributed_execution_present=unattributed_execution_present,
            requested_at=requested_at,
            collection_started_at=collection_started_at,
        )

        return event_execution_reconciliation
