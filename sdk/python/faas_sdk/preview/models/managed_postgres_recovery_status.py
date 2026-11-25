from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_recovery_status_outcome import (
    ManagedPostgresRecoveryStatusOutcome,
    check_managed_postgres_recovery_status_outcome,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresRecoveryStatus")


@_attrs_define
class ManagedPostgresRecoveryStatus:
    region: str
    outcome: ManagedPostgresRecoveryStatusOutcome
    reason: str
    recovery_point: datetime.datetime | Unset = UNSET
    recovery_lag_seconds: int | Unset = UNSET
    missing_wal_segment: str | Unset = UNSET
    recovery_timeline: int | Unset = UNSET
    """PostgreSQL timeline selected from the archive."""
    missing_timeline_history: str | Unset = UNSET
    """Timeline history file needed to select the recovery WAL."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        region = self.region

        outcome: str = self.outcome

        reason = self.reason

        recovery_point: str | Unset = UNSET
        if not isinstance(self.recovery_point, Unset):
            recovery_point = self.recovery_point.isoformat()

        recovery_lag_seconds = self.recovery_lag_seconds

        missing_wal_segment = self.missing_wal_segment

        recovery_timeline = self.recovery_timeline

        missing_timeline_history = self.missing_timeline_history

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "region": region,
                "outcome": outcome,
                "reason": reason,
            }
        )
        if recovery_point is not UNSET:
            field_dict["recovery_point"] = recovery_point
        if recovery_lag_seconds is not UNSET:
            field_dict["recovery_lag_seconds"] = recovery_lag_seconds
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
        region = d.pop("region")

        outcome = check_managed_postgres_recovery_status_outcome(d.pop("outcome"))

        reason = d.pop("reason")

        _recovery_point = d.pop("recovery_point", UNSET)
        recovery_point: datetime.datetime | Unset
        if isinstance(_recovery_point, Unset):
            recovery_point = UNSET
        else:
            recovery_point = datetime.datetime.fromisoformat(_recovery_point)

        recovery_lag_seconds = d.pop("recovery_lag_seconds", UNSET)

        missing_wal_segment = d.pop("missing_wal_segment", UNSET)

        recovery_timeline = d.pop("recovery_timeline", UNSET)

        missing_timeline_history = d.pop("missing_timeline_history", UNSET)

        managed_postgres_recovery_status = cls(
            region=region,
            outcome=outcome,
            reason=reason,
            recovery_point=recovery_point,
            recovery_lag_seconds=recovery_lag_seconds,
            missing_wal_segment=missing_wal_segment,
            recovery_timeline=recovery_timeline,
            missing_timeline_history=missing_timeline_history,
        )

        managed_postgres_recovery_status.additional_properties = d
        return managed_postgres_recovery_status

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
