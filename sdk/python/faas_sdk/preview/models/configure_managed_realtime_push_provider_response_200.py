from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.configure_managed_realtime_push_provider_response_200_provider import (
    ConfigureManagedRealtimePushProviderResponse200Provider,
    check_configure_managed_realtime_push_provider_response_200_provider,
)

T = TypeVar("T", bound="ConfigureManagedRealtimePushProviderResponse200")


@_attrs_define
class ConfigureManagedRealtimePushProviderResponse200:
    provider: ConfigureManagedRealtimePushProviderResponse200Provider
    enabled: bool
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        provider: str = self.provider

        enabled = self.enabled

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "provider": provider,
                "enabled": enabled,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        provider = check_configure_managed_realtime_push_provider_response_200_provider(d.pop("provider"))

        enabled = d.pop("enabled")

        configure_managed_realtime_push_provider_response_200 = cls(
            provider=provider,
            enabled=enabled,
        )

        configure_managed_realtime_push_provider_response_200.additional_properties = d
        return configure_managed_realtime_push_provider_response_200

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
