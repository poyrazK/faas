from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="AppScalingSettings")


@_attrs_define
class AppScalingSettings:
    """Stored RAM, CPU, concurrency and instance-floor settings."""

    ram_mb: int
    cpu_millicores: int
    """Stored CPU allowance; zero uses the plan default."""
    max_concurrency: int
    min_instances: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        ram_mb = self.ram_mb

        cpu_millicores = self.cpu_millicores

        max_concurrency = self.max_concurrency

        min_instances = self.min_instances

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "ram_mb": ram_mb,
                "cpu_millicores": cpu_millicores,
                "max_concurrency": max_concurrency,
                "min_instances": min_instances,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        ram_mb = d.pop("ram_mb")

        cpu_millicores = d.pop("cpu_millicores")

        max_concurrency = d.pop("max_concurrency")

        min_instances = d.pop("min_instances")

        app_scaling_settings = cls(
            ram_mb=ram_mb,
            cpu_millicores=cpu_millicores,
            max_concurrency=max_concurrency,
            min_instances=min_instances,
        )

        app_scaling_settings.additional_properties = d
        return app_scaling_settings

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
