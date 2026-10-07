from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="PutGlobalCellRequest")


@_attrs_define
class PutGlobalCellRequest:
    """Complete operator registry policy. UUID and region cannot change after registration."""

    region: str
    api_base: str
    """Canonical HTTPS API origin."""
    enabled: bool
    apps_domain: str | Unset = UNSET
    """Canonical regional apps DNS suffix. Omission preserves the registered suffix; an explicit empty string
    clears it. A change requires attachment verification again."""

    def to_dict(self) -> dict[str, Any]:
        region = self.region

        api_base = self.api_base

        enabled = self.enabled

        apps_domain = self.apps_domain

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "region": region,
                "api_base": api_base,
                "enabled": enabled,
            }
        )
        if apps_domain is not UNSET:
            field_dict["apps_domain"] = apps_domain

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        region = d.pop("region")

        api_base = d.pop("api_base")

        enabled = d.pop("enabled")

        apps_domain = d.pop("apps_domain", UNSET)

        put_global_cell_request = cls(
            region=region,
            api_base=api_base,
            enabled=enabled,
            apps_domain=apps_domain,
        )

        return put_global_cell_request
