from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RunBudgetLimits")


@_attrs_define
class RunBudgetLimits:
    """Optional concurrency, invocation and compute caps for a customer or task."""

    max_concurrent: int | Unset = UNSET
    max_invocations_per_day: int | Unset = UNSET
    max_compute_mb_ms_per_day: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        max_concurrent = self.max_concurrent

        max_invocations_per_day = self.max_invocations_per_day

        max_compute_mb_ms_per_day = self.max_compute_mb_ms_per_day

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if max_concurrent is not UNSET:
            field_dict["max_concurrent"] = max_concurrent
        if max_invocations_per_day is not UNSET:
            field_dict["max_invocations_per_day"] = max_invocations_per_day
        if max_compute_mb_ms_per_day is not UNSET:
            field_dict["max_compute_mb_ms_per_day"] = max_compute_mb_ms_per_day

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        max_concurrent = d.pop("max_concurrent", UNSET)

        max_invocations_per_day = d.pop("max_invocations_per_day", UNSET)

        max_compute_mb_ms_per_day = d.pop("max_compute_mb_ms_per_day", UNSET)

        run_budget_limits = cls(
            max_concurrent=max_concurrent,
            max_invocations_per_day=max_invocations_per_day,
            max_compute_mb_ms_per_day=max_compute_mb_ms_per_day,
        )

        run_budget_limits.additional_properties = d
        return run_budget_limits

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
