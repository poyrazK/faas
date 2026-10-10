from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_realtime_push_provider_provider import (
    ManagedRealtimePushProviderProvider,
    check_managed_realtime_push_provider_provider,
)

T = TypeVar("T", bound="ManagedRealtimePushProvider")


@_attrs_define
class ManagedRealtimePushProvider:
    """Configured push provider metadata with credentials omitted."""

    provider: ManagedRealtimePushProviderProvider
    enabled: bool
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        provider: str = self.provider

        enabled = self.enabled

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "provider": provider,
                "enabled": enabled,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        provider = check_managed_realtime_push_provider_provider(d.pop("provider"))

        enabled = d.pop("enabled")

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        managed_realtime_push_provider = cls(
            provider=provider,
            enabled=enabled,
            updated_at=updated_at,
        )

        managed_realtime_push_provider.additional_properties = d
        return managed_realtime_push_provider

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
