from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObsRuntimeBuilderCounts")


@_attrs_define
class ObsRuntimeBuilderCounts:
    """Complete/missing/stale/conflicting/unavailable partition target observations. Worker states, waiting queues and
    saturated slot allocators overlap that partition and retain independently usable component evidence. These are
    process-local observations, not fleet capacity or successful builds.

    """

    expected: int
    observed: int
    complete: int
    missing: int
    stale: int
    conflicting: int
    unavailable: int
    unexpected: int
    overdue_workers: int
    waiting_workers: int
    working_workers: int
    draining_workers: int
    waiting_queues: int
    saturated_slots: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        expected = self.expected

        observed = self.observed

        complete = self.complete

        missing = self.missing

        stale = self.stale

        conflicting = self.conflicting

        unavailable = self.unavailable

        unexpected = self.unexpected

        overdue_workers = self.overdue_workers

        waiting_workers = self.waiting_workers

        working_workers = self.working_workers

        draining_workers = self.draining_workers

        waiting_queues = self.waiting_queues

        saturated_slots = self.saturated_slots

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "expected": expected,
                "observed": observed,
                "complete": complete,
                "missing": missing,
                "stale": stale,
                "conflicting": conflicting,
                "unavailable": unavailable,
                "unexpected": unexpected,
                "overdue_workers": overdue_workers,
                "waiting_workers": waiting_workers,
                "working_workers": working_workers,
                "draining_workers": draining_workers,
                "waiting_queues": waiting_queues,
                "saturated_slots": saturated_slots,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected = d.pop("expected")

        observed = d.pop("observed")

        complete = d.pop("complete")

        missing = d.pop("missing")

        stale = d.pop("stale")

        conflicting = d.pop("conflicting")

        unavailable = d.pop("unavailable")

        unexpected = d.pop("unexpected")

        overdue_workers = d.pop("overdue_workers")

        waiting_workers = d.pop("waiting_workers")

        working_workers = d.pop("working_workers")

        draining_workers = d.pop("draining_workers")

        waiting_queues = d.pop("waiting_queues")

        saturated_slots = d.pop("saturated_slots")

        obs_runtime_builder_counts = cls(
            expected=expected,
            observed=observed,
            complete=complete,
            missing=missing,
            stale=stale,
            conflicting=conflicting,
            unavailable=unavailable,
            unexpected=unexpected,
            overdue_workers=overdue_workers,
            waiting_workers=waiting_workers,
            working_workers=working_workers,
            draining_workers=draining_workers,
            waiting_queues=waiting_queues,
            saturated_slots=saturated_slots,
        )

        obs_runtime_builder_counts.additional_properties = d
        return obs_runtime_builder_counts

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
