from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_realtime_push_device_provider import (
    ManagedRealtimePushDeviceProvider,
    check_managed_realtime_push_device_provider,
)

T = TypeVar("T", bound="ManagedRealtimePushDevice")


@_attrs_define
class ManagedRealtimePushDevice:
    """Registered device metadata with delivery tokens omitted."""

    device: str
    provider: ManagedRealtimePushDeviceProvider
    enabled: bool
    version: int
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        device = self.device

        provider: str = self.provider

        enabled = self.enabled

        version = self.version

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "device": device,
                "provider": provider,
                "enabled": enabled,
                "version": version,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        device = d.pop("device")

        provider = check_managed_realtime_push_device_provider(d.pop("provider"))

        enabled = d.pop("enabled")

        version = d.pop("version")

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        managed_realtime_push_device = cls(
            device=device,
            provider=provider,
            enabled=enabled,
            version=version,
            updated_at=updated_at,
        )

        managed_realtime_push_device.additional_properties = d
        return managed_realtime_push_device

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
