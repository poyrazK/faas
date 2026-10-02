from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RouteCheckChangeSummary")


@_attrs_define
class RouteCheckChangeSummary:
    newly_violated: int
    resolved: int
    changed: int
    unknown: int
    removed: int
    observed: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        newly_violated = self.newly_violated

        resolved = self.resolved

        changed = self.changed

        unknown = self.unknown

        removed = self.removed

        observed = self.observed

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "newly_violated": newly_violated,
                "resolved": resolved,
                "changed": changed,
                "unknown": unknown,
                "removed": removed,
                "observed": observed,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        newly_violated = d.pop("newly_violated")

        resolved = d.pop("resolved")

        changed = d.pop("changed")

        unknown = d.pop("unknown")

        removed = d.pop("removed")

        observed = d.pop("observed")

        route_check_change_summary = cls(
            newly_violated=newly_violated,
            resolved=resolved,
            changed=changed,
            unknown=unknown,
            removed=removed,
            observed=observed,
        )

        route_check_change_summary.additional_properties = d
        return route_check_change_summary

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
