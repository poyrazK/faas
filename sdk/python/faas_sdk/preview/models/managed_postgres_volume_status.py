from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ManagedPostgresVolumeStatus")


@_attrs_define
class ManagedPostgresVolumeStatus:
    """Last observed filesystem capacity and pressure for a native database volume."""

    total_bytes: int
    free_bytes: int
    low: bool
    """Whether free space was below the storage guard recovery threshold."""
    read_only: bool
    """Whether the primary storage guard was in read-only mode."""
    sampled_at: datetime.datetime
    fresh: bool
    """Whether the observation is recent and belongs to the current running placement."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        total_bytes = self.total_bytes

        free_bytes = self.free_bytes

        low = self.low

        read_only = self.read_only

        sampled_at = self.sampled_at.isoformat()

        fresh = self.fresh

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "total_bytes": total_bytes,
                "free_bytes": free_bytes,
                "low": low,
                "read_only": read_only,
                "sampled_at": sampled_at,
                "fresh": fresh,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        total_bytes = d.pop("total_bytes")

        free_bytes = d.pop("free_bytes")

        low = d.pop("low")

        read_only = d.pop("read_only")

        sampled_at = datetime.datetime.fromisoformat(d.pop("sampled_at"))

        fresh = d.pop("fresh")

        managed_postgres_volume_status = cls(
            total_bytes=total_bytes,
            free_bytes=free_bytes,
            low=low,
            read_only=read_only,
            sampled_at=sampled_at,
            fresh=fresh,
        )

        managed_postgres_volume_status.additional_properties = d
        return managed_postgres_volume_status

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
