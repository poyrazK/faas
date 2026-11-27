from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_restore_check_outcome import (
    ManagedPostgresRestoreCheckOutcome,
    check_managed_postgres_restore_check_outcome,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresRestoreCheck")


@_attrs_define
class ManagedPostgresRestoreCheck:
    """Archive evidence for a native PostgreSQL point-in-time restore target."""

    point_in_time: datetime.datetime
    outcome: ManagedPostgresRestoreCheckOutcome
    reason: str
    """Stable reason code for the archive check."""
    backup_completed_at: datetime.datetime | Unset = UNSET
    wal_stop_at: datetime.datetime | Unset = UNSET
    """Timestamp of the archived transaction record that would let PostgreSQL stop replay at the target."""
    missing_wal_segment: str | Unset = UNSET
    """WAL segment required by the selected restore path and missing from the archive."""
    recovery_timeline: int | Unset = UNSET
    """PostgreSQL timeline selected from the archive."""
    missing_timeline_history: str | Unset = UNSET
    """Timeline history file needed to select the recovery WAL."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        point_in_time = self.point_in_time.isoformat()

        outcome: str = self.outcome

        reason = self.reason

        backup_completed_at: str | Unset = UNSET
        if not isinstance(self.backup_completed_at, Unset):
            backup_completed_at = self.backup_completed_at.isoformat()

        wal_stop_at: str | Unset = UNSET
        if not isinstance(self.wal_stop_at, Unset):
            wal_stop_at = self.wal_stop_at.isoformat()

        missing_wal_segment = self.missing_wal_segment

        recovery_timeline = self.recovery_timeline

        missing_timeline_history = self.missing_timeline_history

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "point_in_time": point_in_time,
                "outcome": outcome,
                "reason": reason,
            }
        )
        if backup_completed_at is not UNSET:
            field_dict["backup_completed_at"] = backup_completed_at
        if wal_stop_at is not UNSET:
            field_dict["wal_stop_at"] = wal_stop_at
        if missing_wal_segment is not UNSET:
            field_dict["missing_wal_segment"] = missing_wal_segment
        if recovery_timeline is not UNSET:
            field_dict["recovery_timeline"] = recovery_timeline
        if missing_timeline_history is not UNSET:
            field_dict["missing_timeline_history"] = missing_timeline_history

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        point_in_time = datetime.datetime.fromisoformat(d.pop("point_in_time"))

        outcome = check_managed_postgres_restore_check_outcome(d.pop("outcome"))

        reason = d.pop("reason")

        _backup_completed_at = d.pop("backup_completed_at", UNSET)
        backup_completed_at: datetime.datetime | Unset
        if isinstance(_backup_completed_at, Unset):
            backup_completed_at = UNSET
        else:
            backup_completed_at = datetime.datetime.fromisoformat(_backup_completed_at)

        _wal_stop_at = d.pop("wal_stop_at", UNSET)
        wal_stop_at: datetime.datetime | Unset
        if isinstance(_wal_stop_at, Unset):
            wal_stop_at = UNSET
        else:
            wal_stop_at = datetime.datetime.fromisoformat(_wal_stop_at)

        missing_wal_segment = d.pop("missing_wal_segment", UNSET)

        recovery_timeline = d.pop("recovery_timeline", UNSET)

        missing_timeline_history = d.pop("missing_timeline_history", UNSET)

        managed_postgres_restore_check = cls(
            point_in_time=point_in_time,
            outcome=outcome,
            reason=reason,
            backup_completed_at=backup_completed_at,
            wal_stop_at=wal_stop_at,
            missing_wal_segment=missing_wal_segment,
            recovery_timeline=recovery_timeline,
            missing_timeline_history=missing_timeline_history,
        )

        managed_postgres_restore_check.additional_properties = d
        return managed_postgres_restore_check

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
