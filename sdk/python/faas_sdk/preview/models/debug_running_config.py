from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DebugRunningConfig")


@_attrs_define
class DebugRunningConfig:
    """Configuration context shown with the observed running causes."""

    configured_min_instances: int
    effective_min_instances: int
    idle_timeout_seconds: int
    prewarm_min_instances: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        configured_min_instances = self.configured_min_instances

        effective_min_instances = self.effective_min_instances

        idle_timeout_seconds = self.idle_timeout_seconds

        prewarm_min_instances = self.prewarm_min_instances

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "configured_min_instances": configured_min_instances,
                "effective_min_instances": effective_min_instances,
                "idle_timeout_seconds": idle_timeout_seconds,
            }
        )
        if prewarm_min_instances is not UNSET:
            field_dict["prewarm_min_instances"] = prewarm_min_instances

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        configured_min_instances = d.pop("configured_min_instances")

        effective_min_instances = d.pop("effective_min_instances")

        idle_timeout_seconds = d.pop("idle_timeout_seconds")

        prewarm_min_instances = d.pop("prewarm_min_instances", UNSET)

        debug_running_config = cls(
            configured_min_instances=configured_min_instances,
            effective_min_instances=effective_min_instances,
            idle_timeout_seconds=idle_timeout_seconds,
            prewarm_min_instances=prewarm_min_instances,
        )

        debug_running_config.additional_properties = d
        return debug_running_config

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
