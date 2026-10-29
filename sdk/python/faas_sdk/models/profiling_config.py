from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.profiling_config_kinds_item import ProfilingConfigKindsItem, check_profiling_config_kinds_item
from ..types import UNSET, Unset

T = TypeVar("T", bound="ProfilingConfig")


@_attrs_define
class ProfilingConfig:
    """Opt-in sampled CPU profiling baked into each deployment."""

    enabled: bool
    window_seconds: int | Unset = 10
    kinds: list[ProfilingConfigKindsItem] | Unset = UNSET
    """Continuous profile kinds; defaults to cpu."""
    pprof_port: int | Unset = UNSET
    """Guest-local port where a Go application serves net/http/pprof. The platform then collects CPU, heap and
    goroutine profiles from it, so the application needs no Gregale SDK. 9191 and 4318 are reserved."""
    automatic_captures: bool | Unset = UNSET
    """Whether the platform may start automatic on-demand captures of the app when memory runs low, a request is in
    flight for too long, or an instance uses its whole CPU limit. Unset means on. Automatic captures count against
    their own hourly limit and at most half of the account's capture storage."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        window_seconds = self.window_seconds

        kinds: list[str] | Unset = UNSET
        if not isinstance(self.kinds, Unset):
            kinds = []
            for kinds_item_data in self.kinds:
                kinds_item: str = kinds_item_data
                kinds.append(kinds_item)

        pprof_port = self.pprof_port

        automatic_captures = self.automatic_captures

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "enabled": enabled,
            }
        )
        if window_seconds is not UNSET:
            field_dict["window_seconds"] = window_seconds
        if kinds is not UNSET:
            field_dict["kinds"] = kinds
        if pprof_port is not UNSET:
            field_dict["pprof_port"] = pprof_port
        if automatic_captures is not UNSET:
            field_dict["automatic_captures"] = automatic_captures

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        enabled = d.pop("enabled")

        window_seconds = d.pop("window_seconds", UNSET)

        _kinds = d.pop("kinds", UNSET)
        kinds: list[ProfilingConfigKindsItem] | Unset = UNSET
        if _kinds is not UNSET:
            kinds = []
            for kinds_item_data in _kinds:
                kinds_item = check_profiling_config_kinds_item(kinds_item_data)

                kinds.append(kinds_item)

        pprof_port = d.pop("pprof_port", UNSET)

        automatic_captures = d.pop("automatic_captures", UNSET)

        profiling_config = cls(
            enabled=enabled,
            window_seconds=window_seconds,
            kinds=kinds,
            pprof_port=pprof_port,
            automatic_captures=automatic_captures,
        )

        profiling_config.additional_properties = d
        return profiling_config

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
