from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresReplicaStatus")


@_attrs_define
class ManagedPostgresReplicaStatus:
    """Standby health sampled without waking a native production database."""

    available: bool
    """Whether the standby was available for reads when observed."""
    replay_lag_bytes: int | Unset = UNSET
    """WAL backlog between primary flush and standby replay. Omitted when unknown."""
    observed_at: datetime.datetime | Unset = UNSET
    """When the replay evidence was collected."""
    sample_age_ms: int | Unset = UNSET
    """Age in milliseconds when the response was built."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        available = self.available

        replay_lag_bytes = self.replay_lag_bytes

        observed_at: str | Unset = UNSET
        if not isinstance(self.observed_at, Unset):
            observed_at = self.observed_at.isoformat()

        sample_age_ms = self.sample_age_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "available": available,
            }
        )
        if replay_lag_bytes is not UNSET:
            field_dict["replay_lag_bytes"] = replay_lag_bytes
        if observed_at is not UNSET:
            field_dict["observed_at"] = observed_at
        if sample_age_ms is not UNSET:
            field_dict["sample_age_ms"] = sample_age_ms

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        available = d.pop("available")

        replay_lag_bytes = d.pop("replay_lag_bytes", UNSET)

        _observed_at = d.pop("observed_at", UNSET)
        observed_at: datetime.datetime | Unset
        if isinstance(_observed_at, Unset):
            observed_at = UNSET
        else:
            observed_at = datetime.datetime.fromisoformat(_observed_at)

        sample_age_ms = d.pop("sample_age_ms", UNSET)

        managed_postgres_replica_status = cls(
            available=available,
            replay_lag_bytes=replay_lag_bytes,
            observed_at=observed_at,
            sample_age_ms=sample_age_ms,
        )

        managed_postgres_replica_status.additional_properties = d
        return managed_postgres_replica_status

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
