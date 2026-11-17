from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObsRuntimePreparationCounts")


@_attrs_define
class ObsRuntimePreparationCounts:
    """Complete/missing/stale/conflicting/unavailable partition targets. Remaining counts overlap that partition. Complete
    means usable components; historical ages may still be unknown. Waiting scopes have any observed pending phase; empty
    scopes have all three phases known empty. Disabled observation never implies drained work.

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
    unknown_age_scopes: int
    unsuccessful_collections: int
    enabled_scopes: int
    disabled_scopes: int
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

        unknown_age_scopes = self.unknown_age_scopes

        unsuccessful_collections = self.unsuccessful_collections

        enabled_scopes = self.enabled_scopes

        disabled_scopes = self.disabled_scopes

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
                "unknown_age_scopes": unknown_age_scopes,
                "unsuccessful_collections": unsuccessful_collections,
                "enabled_scopes": enabled_scopes,
                "disabled_scopes": disabled_scopes,
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

        unknown_age_scopes = d.pop("unknown_age_scopes")

        unsuccessful_collections = d.pop("unsuccessful_collections")

        enabled_scopes = d.pop("enabled_scopes")

        disabled_scopes = d.pop("disabled_scopes")

        obs_runtime_preparation_counts = cls(
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
            unknown_age_scopes=unknown_age_scopes,
            unsuccessful_collections=unsuccessful_collections,
            enabled_scopes=enabled_scopes,
            disabled_scopes=disabled_scopes,
        )

        obs_runtime_preparation_counts.additional_properties = d
        return obs_runtime_preparation_counts

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
