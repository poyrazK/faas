from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.app_health_history_entry_kind import AppHealthHistoryEntryKind, check_app_health_history_entry_kind
from ..models.app_health_history_entry_previous_status import (
    AppHealthHistoryEntryPreviousStatus,
    check_app_health_history_entry_previous_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_health_response import AppHealthResponse


T = TypeVar("T", bound="AppHealthHistoryEntry")


@_attrs_define
class AppHealthHistoryEntry:
    """Original baseline, meaningful transition, or expired-evidence gap; times do not imply continuous incident duration."""

    id: UUID
    kind: AppHealthHistoryEntryKind
    observed_at: datetime.datetime
    """Assessment time, or previous evidence expiry for a gap entry."""
    assessment: AppHealthResponse
    """Read-only observed health of default-scope HTTP serving workloads."""
    previous_status: AppHealthHistoryEntryPreviousStatus | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        kind: str = self.kind

        observed_at = self.observed_at.isoformat()

        assessment = self.assessment.to_dict()

        previous_status: str | Unset = UNSET
        if not isinstance(self.previous_status, Unset):
            previous_status = self.previous_status

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "kind": kind,
                "observed_at": observed_at,
                "assessment": assessment,
            }
        )
        if previous_status is not UNSET:
            field_dict["previous_status"] = previous_status

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_health_response import AppHealthResponse

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        kind = check_app_health_history_entry_kind(d.pop("kind"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        assessment = AppHealthResponse.from_dict(d.pop("assessment"))

        _previous_status = d.pop("previous_status", UNSET)
        previous_status: AppHealthHistoryEntryPreviousStatus | Unset
        if isinstance(_previous_status, Unset):
            previous_status = UNSET
        else:
            previous_status = check_app_health_history_entry_previous_status(_previous_status)

        app_health_history_entry = cls(
            id=id,
            kind=kind,
            observed_at=observed_at,
            assessment=assessment,
            previous_status=previous_status,
        )

        return app_health_history_entry
