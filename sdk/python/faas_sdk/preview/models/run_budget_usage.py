from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RunBudgetUsage")


@_attrs_define
class RunBudgetUsage:
    """Active reservations and metered usage in the current UTC-day window."""

    day: datetime.date
    active: int
    invocations: int
    compute_used_mb_ms: int
    compute_reserved_mb_ms: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        day = self.day.isoformat()

        active = self.active

        invocations = self.invocations

        compute_used_mb_ms = self.compute_used_mb_ms

        compute_reserved_mb_ms = self.compute_reserved_mb_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "day": day,
                "active": active,
                "invocations": invocations,
                "compute_used_mb_ms": compute_used_mb_ms,
                "compute_reserved_mb_ms": compute_reserved_mb_ms,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        day = datetime.date.fromisoformat(d.pop("day"))

        active = d.pop("active")

        invocations = d.pop("invocations")

        compute_used_mb_ms = d.pop("compute_used_mb_ms")

        compute_reserved_mb_ms = d.pop("compute_reserved_mb_ms")

        run_budget_usage = cls(
            day=day,
            active=active,
            invocations=invocations,
            compute_used_mb_ms=compute_used_mb_ms,
            compute_reserved_mb_ms=compute_reserved_mb_ms,
        )

        run_budget_usage.additional_properties = d
        return run_budget_usage

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
