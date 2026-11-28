from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObsRuntimeReadinessCounts")


@_attrs_define
class ObsRuntimeReadinessCounts:
    """Readiness states partition the full inspected set before preview truncation."""

    ready: int
    not_ready: int
    missing: int
    stale: int
    conflicting: int
    unavailable: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        ready = self.ready

        not_ready = self.not_ready

        missing = self.missing

        stale = self.stale

        conflicting = self.conflicting

        unavailable = self.unavailable

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "ready": ready,
                "not_ready": not_ready,
                "missing": missing,
                "stale": stale,
                "conflicting": conflicting,
                "unavailable": unavailable,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        ready = d.pop("ready")

        not_ready = d.pop("not_ready")

        missing = d.pop("missing")

        stale = d.pop("stale")

        conflicting = d.pop("conflicting")

        unavailable = d.pop("unavailable")

        obs_runtime_readiness_counts = cls(
            ready=ready,
            not_ready=not_ready,
            missing=missing,
            stale=stale,
            conflicting=conflicting,
            unavailable=unavailable,
        )

        obs_runtime_readiness_counts.additional_properties = d
        return obs_runtime_readiness_counts

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
