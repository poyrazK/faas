from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="EnvironmentServiceBinding")


@_attrs_define
class EnvironmentServiceBinding:
    """Reference to another logical workload in the same environment."""

    workload: str
    env_key: str

    def to_dict(self) -> dict[str, Any]:
        workload = self.workload

        env_key = self.env_key

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "workload": workload,
                "env_key": env_key,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        workload = d.pop("workload")

        env_key = d.pop("env_key")

        environment_service_binding = cls(
            workload=workload,
            env_key=env_key,
        )

        return environment_service_binding
