from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="BindingRuntimeInstanceCounts")


@_attrs_define
class BindingRuntimeInstanceCounts:
    """Disjoint counts compared with the app-wide change stamp. Current means admitted after the stamp; stale means
    admitted at or before it; unknown means missing stamp or start timestamp.

    """

    current: int
    stale: int
    unknown: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        current = self.current

        stale = self.stale

        unknown = self.unknown

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "current": current,
                "stale": stale,
                "unknown": unknown,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        current = d.pop("current")

        stale = d.pop("stale")

        unknown = d.pop("unknown")

        binding_runtime_instance_counts = cls(
            current=current,
            stale=stale,
            unknown=unknown,
        )

        binding_runtime_instance_counts.additional_properties = d
        return binding_runtime_instance_counts

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
