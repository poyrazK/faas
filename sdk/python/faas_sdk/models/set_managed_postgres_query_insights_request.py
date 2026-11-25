from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="SetManagedPostgresQueryInsightsRequest")


@_attrs_define
class SetManagedPostgresQueryInsightsRequest:
    """Controls collection of aggregate statement statistics at eligible restarts."""

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

        set_managed_postgres_query_insights_request = cls(
            enabled=enabled,
        )

        return set_managed_postgres_query_insights_request
