from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="SetManagedPostgresRecoveryRegionRequest")


@_attrs_define
class SetManagedPostgresRecoveryRegionRequest:
    region: str
    """The configured recovery destination. Send an empty string to disable regional recovery."""

    def to_dict(self) -> dict[str, Any]:
        region = self.region

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "region": region,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        region = d.pop("region")

        set_managed_postgres_recovery_region_request = cls(
            region=region,
        )

        return set_managed_postgres_recovery_region_request
