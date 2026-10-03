from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="PutGlobalCellRequest")


@_attrs_define
class PutGlobalCellRequest:
    """Complete operator registry policy. UUID and region cannot change after registration."""

    region: str
    api_base: str
    """Canonical HTTPS API origin."""
    enabled: bool

    def to_dict(self) -> dict[str, Any]:
        region = self.region

        api_base = self.api_base

        enabled = self.enabled

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "region": region,
                "api_base": api_base,
                "enabled": enabled,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        region = d.pop("region")

        api_base = d.pop("api_base")

        enabled = d.pop("enabled")

        put_global_cell_request = cls(
            region=region,
            api_base=api_base,
            enabled=enabled,
        )

        return put_global_cell_request
