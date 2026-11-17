from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObsRuntimeAppTaskCounts")


@_attrs_define
class ObsRuntimeAppTaskCounts:
    """Replica-view coverage and task evidence across the full inspected set before clipping. Nonempty means any positive
    task class; empty requires all 27 kind/class observations empty. Observer expectation and local dispatcher activity
    are independent. Running and stopped runner views partition dispatch-enabled views; occupied and saturated views
    describe local handler slots.

    """

    expected: int
    observed: int
    complete: int
    missing: int
    stale: int
    conflicting: int
    unavailable: int
    unexpected: int
    nonempty_views: int
    empty_views: int
    unknown_age_views: int
    unsuccessful_collections: int
    enabled_views: int
    disabled_views: int
    dispatch_enabled_views: int
    dispatch_disabled_views: int
    runner_running_views: int
    runner_stopped_views: int
    occupied_views: int
    saturated_views: int
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

        nonempty_views = self.nonempty_views

        empty_views = self.empty_views

        unknown_age_views = self.unknown_age_views

        unsuccessful_collections = self.unsuccessful_collections

        enabled_views = self.enabled_views

        disabled_views = self.disabled_views

        dispatch_enabled_views = self.dispatch_enabled_views

        dispatch_disabled_views = self.dispatch_disabled_views

        runner_running_views = self.runner_running_views

        runner_stopped_views = self.runner_stopped_views

        occupied_views = self.occupied_views

        saturated_views = self.saturated_views

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
                "nonempty_views": nonempty_views,
                "empty_views": empty_views,
                "unknown_age_views": unknown_age_views,
                "unsuccessful_collections": unsuccessful_collections,
                "enabled_views": enabled_views,
                "disabled_views": disabled_views,
                "dispatch_enabled_views": dispatch_enabled_views,
                "dispatch_disabled_views": dispatch_disabled_views,
                "runner_running_views": runner_running_views,
                "runner_stopped_views": runner_stopped_views,
                "occupied_views": occupied_views,
                "saturated_views": saturated_views,
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

        nonempty_views = d.pop("nonempty_views")

        empty_views = d.pop("empty_views")

        unknown_age_views = d.pop("unknown_age_views")

        unsuccessful_collections = d.pop("unsuccessful_collections")

        enabled_views = d.pop("enabled_views")

        disabled_views = d.pop("disabled_views")

        dispatch_enabled_views = d.pop("dispatch_enabled_views")

        dispatch_disabled_views = d.pop("dispatch_disabled_views")

        runner_running_views = d.pop("runner_running_views")

        runner_stopped_views = d.pop("runner_stopped_views")

        occupied_views = d.pop("occupied_views")

        saturated_views = d.pop("saturated_views")

        obs_runtime_app_task_counts = cls(
            expected=expected,
            observed=observed,
            complete=complete,
            missing=missing,
            stale=stale,
            conflicting=conflicting,
            unavailable=unavailable,
            unexpected=unexpected,
            nonempty_views=nonempty_views,
            empty_views=empty_views,
            unknown_age_views=unknown_age_views,
            unsuccessful_collections=unsuccessful_collections,
            enabled_views=enabled_views,
            disabled_views=disabled_views,
            dispatch_enabled_views=dispatch_enabled_views,
            dispatch_disabled_views=dispatch_disabled_views,
            runner_running_views=runner_running_views,
            runner_stopped_views=runner_stopped_views,
            occupied_views=occupied_views,
            saturated_views=saturated_views,
        )

        obs_runtime_app_task_counts.additional_properties = d
        return obs_runtime_app_task_counts

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
