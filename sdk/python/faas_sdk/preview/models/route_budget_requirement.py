from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="RouteBudgetRequirement")


@_attrs_define
class RouteBudgetRequirement:
    """Optional execution-budget ceiling or requirement for an explicit budget rule."""

    max_ms: int | Unset = UNSET
    explicit: bool | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        max_ms = self.max_ms

        explicit = self.explicit

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if max_ms is not UNSET:
            field_dict["max_ms"] = max_ms
        if explicit is not UNSET:
            field_dict["explicit"] = explicit

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        max_ms = d.pop("max_ms", UNSET)

        explicit = d.pop("explicit", UNSET)

        route_budget_requirement = cls(
            max_ms=max_ms,
            explicit=explicit,
        )

        return route_budget_requirement
