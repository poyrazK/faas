from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ExecutionEnvironmentLimits")


@_attrs_define
class ExecutionEnvironmentLimits:
    """Dependency recipe and image limits for the current account."""

    max_environments: int
    max_artifact_bytes: int
    max_recipe_bytes: int
    max_packages: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        max_environments = self.max_environments

        max_artifact_bytes = self.max_artifact_bytes

        max_recipe_bytes = self.max_recipe_bytes

        max_packages = self.max_packages

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "max_environments": max_environments,
                "max_artifact_bytes": max_artifact_bytes,
                "max_recipe_bytes": max_recipe_bytes,
                "max_packages": max_packages,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        max_environments = d.pop("max_environments")

        max_artifact_bytes = d.pop("max_artifact_bytes")

        max_recipe_bytes = d.pop("max_recipe_bytes")

        max_packages = d.pop("max_packages")

        execution_environment_limits = cls(
            max_environments=max_environments,
            max_artifact_bytes=max_artifact_bytes,
            max_recipe_bytes=max_recipe_bytes,
            max_packages=max_packages,
        )

        execution_environment_limits.additional_properties = d
        return execution_environment_limits

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
