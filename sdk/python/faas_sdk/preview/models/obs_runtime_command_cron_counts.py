from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObsRuntimeCommandCronCounts")


@_attrs_define
class ObsRuntimeCommandCronCounts:
    """Full-set scheduler-scope coverage, including overlapping scopes. Nonempty requires any observed schedule, including
    disabled or not-due intent; empty requires all ten classes observed empty. Due scopes contain any first-five class
    schedules; inactive scopes contain disabled, suspended, account-held or not-due schedules. Those categories can
    overlap. Sweep categories describe observed scan activity, not command delivery.

    """

    expected: int
    observed: int
    complete: int
    missing: int
    stale: int
    conflicting: int
    unavailable: int
    unexpected: int
    nonempty_scopes: int
    empty_scopes: int
    unknown_age_scopes: int
    unsuccessful_collections: int
    enabled_scopes: int
    disabled_scopes: int
    due_scopes: int
    inactive_scopes: int
    sweep_running_scopes: int
    sweep_idle_scopes: int
    sweep_never_started_scopes: int
    sweep_never_succeeded_scopes: int
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

        nonempty_scopes = self.nonempty_scopes

        empty_scopes = self.empty_scopes

        unknown_age_scopes = self.unknown_age_scopes

        unsuccessful_collections = self.unsuccessful_collections

        enabled_scopes = self.enabled_scopes

        disabled_scopes = self.disabled_scopes

        due_scopes = self.due_scopes

        inactive_scopes = self.inactive_scopes

        sweep_running_scopes = self.sweep_running_scopes

        sweep_idle_scopes = self.sweep_idle_scopes

        sweep_never_started_scopes = self.sweep_never_started_scopes

        sweep_never_succeeded_scopes = self.sweep_never_succeeded_scopes

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
                "nonempty_scopes": nonempty_scopes,
                "empty_scopes": empty_scopes,
                "unknown_age_scopes": unknown_age_scopes,
                "unsuccessful_collections": unsuccessful_collections,
                "enabled_scopes": enabled_scopes,
                "disabled_scopes": disabled_scopes,
                "due_scopes": due_scopes,
                "inactive_scopes": inactive_scopes,
                "sweep_running_scopes": sweep_running_scopes,
                "sweep_idle_scopes": sweep_idle_scopes,
                "sweep_never_started_scopes": sweep_never_started_scopes,
                "sweep_never_succeeded_scopes": sweep_never_succeeded_scopes,
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

        nonempty_scopes = d.pop("nonempty_scopes")

        empty_scopes = d.pop("empty_scopes")

        unknown_age_scopes = d.pop("unknown_age_scopes")

        unsuccessful_collections = d.pop("unsuccessful_collections")

        enabled_scopes = d.pop("enabled_scopes")

        disabled_scopes = d.pop("disabled_scopes")

        due_scopes = d.pop("due_scopes")

        inactive_scopes = d.pop("inactive_scopes")

        sweep_running_scopes = d.pop("sweep_running_scopes")

        sweep_idle_scopes = d.pop("sweep_idle_scopes")

        sweep_never_started_scopes = d.pop("sweep_never_started_scopes")

        sweep_never_succeeded_scopes = d.pop("sweep_never_succeeded_scopes")

        obs_runtime_command_cron_counts = cls(
            expected=expected,
            observed=observed,
            complete=complete,
            missing=missing,
            stale=stale,
            conflicting=conflicting,
            unavailable=unavailable,
            unexpected=unexpected,
            nonempty_scopes=nonempty_scopes,
            empty_scopes=empty_scopes,
            unknown_age_scopes=unknown_age_scopes,
            unsuccessful_collections=unsuccessful_collections,
            enabled_scopes=enabled_scopes,
            disabled_scopes=disabled_scopes,
            due_scopes=due_scopes,
            inactive_scopes=inactive_scopes,
            sweep_running_scopes=sweep_running_scopes,
            sweep_idle_scopes=sweep_idle_scopes,
            sweep_never_started_scopes=sweep_never_started_scopes,
            sweep_never_succeeded_scopes=sweep_never_succeeded_scopes,
        )

        obs_runtime_command_cron_counts.additional_properties = d
        return obs_runtime_command_cron_counts

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
