from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="ManagedPostgresOverageRequest")


@_attrs_define
class ManagedPostgresOverageRequest:
    enabled: bool

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "enabled": enabled,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        enabled = d.pop("enabled")

        managed_postgres_overage_request = cls(
            enabled=enabled,
        )

        return managed_postgres_overage_request
