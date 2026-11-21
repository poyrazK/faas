from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObsRuntimeHandoffCounts")


@_attrs_define
class ObsRuntimeHandoffCounts:
    """Complete/missing/stale/conflicting/unavailable partition inspected targets. Backlog and recovery counts overlap that
    partition and count observation scopes, not unique builds. Unsuccessful collections count fresh zero success
    reports, including observers without any successful read yet.

    """

    expected: int
    observed: int
    complete: int
    missing: int
    stale: int
    conflicting: int
    unavailable: int
    unexpected: int
    waiting_scopes: int
    empty_scopes: int
    unsuccessful_collections: int
    working_recovery_scopes: int
    waiting_recovery_scopes: int
    stopping_recovery_scopes: int
    not_started_recovery_scopes: int
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

        waiting_scopes = self.waiting_scopes

        empty_scopes = self.empty_scopes

        unsuccessful_collections = self.unsuccessful_collections

        working_recovery_scopes = self.working_recovery_scopes

        waiting_recovery_scopes = self.waiting_recovery_scopes

        stopping_recovery_scopes = self.stopping_recovery_scopes

        not_started_recovery_scopes = self.not_started_recovery_scopes

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
                "waiting_scopes": waiting_scopes,
                "empty_scopes": empty_scopes,
                "unsuccessful_collections": unsuccessful_collections,
                "working_recovery_scopes": working_recovery_scopes,
                "waiting_recovery_scopes": waiting_recovery_scopes,
                "stopping_recovery_scopes": stopping_recovery_scopes,
                "not_started_recovery_scopes": not_started_recovery_scopes,
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

        waiting_scopes = d.pop("waiting_scopes")

        empty_scopes = d.pop("empty_scopes")

        unsuccessful_collections = d.pop("unsuccessful_collections")

        working_recovery_scopes = d.pop("working_recovery_scopes")

        waiting_recovery_scopes = d.pop("waiting_recovery_scopes")

        stopping_recovery_scopes = d.pop("stopping_recovery_scopes")

        not_started_recovery_scopes = d.pop("not_started_recovery_scopes")

        obs_runtime_handoff_counts = cls(
            expected=expected,
            observed=observed,
            complete=complete,
            missing=missing,
            stale=stale,
            conflicting=conflicting,
            unavailable=unavailable,
            unexpected=unexpected,
            waiting_scopes=waiting_scopes,
            empty_scopes=empty_scopes,
            unsuccessful_collections=unsuccessful_collections,
            working_recovery_scopes=working_recovery_scopes,
            waiting_recovery_scopes=waiting_recovery_scopes,
            stopping_recovery_scopes=stopping_recovery_scopes,
            not_started_recovery_scopes=not_started_recovery_scopes,
        )

        obs_runtime_handoff_counts.additional_properties = d
        return obs_runtime_handoff_counts

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
