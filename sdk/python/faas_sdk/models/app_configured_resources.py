from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_configured_resources_cpu_millicores import (
    AppConfiguredResourcesCpuMillicores,
    check_app_configured_resources_cpu_millicores,
)

T = TypeVar("T", bound="AppConfiguredResources")


@_attrs_define
class AppConfiguredResources:
    """The memory and sustained CPU shape selected for each instance of this app."""

    memory_mb: int
    """Configured instance memory in MB."""
    cpu_millicores: AppConfiguredResourcesCpuMillicores
    """Configured sustained CPU allowance in millicores."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        memory_mb = self.memory_mb

        cpu_millicores: int = self.cpu_millicores

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "memory_mb": memory_mb,
                "cpu_millicores": cpu_millicores,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        memory_mb = d.pop("memory_mb")

        cpu_millicores = check_app_configured_resources_cpu_millicores(d.pop("cpu_millicores"))

        app_configured_resources = cls(
            memory_mb=memory_mb,
            cpu_millicores=cpu_millicores,
        )

        app_configured_resources.additional_properties = d
        return app_configured_resources

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
