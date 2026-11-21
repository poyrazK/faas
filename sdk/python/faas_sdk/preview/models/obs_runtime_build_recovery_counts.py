from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObsRuntimeBuildRecoveryCounts")


@_attrs_define
class ObsRuntimeBuildRecoveryCounts:
    """Complete/missing/stale/conflicting/unavailable partition replica observations. Remaining counts overlap that
    partition and count views, never builds or obligations. Running and cleanup views may overlap; nonempty is their
    union, empty requires both components observed empty, and retried cleanup views are a subset of cleanup views.

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
    running_views: int
    cleanup_views: int
    retried_cleanup_views: int
    unsuccessful_collections: int
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

        running_views = self.running_views

        cleanup_views = self.cleanup_views

        retried_cleanup_views = self.retried_cleanup_views

        unsuccessful_collections = self.unsuccessful_collections

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
                "running_views": running_views,
                "cleanup_views": cleanup_views,
                "retried_cleanup_views": retried_cleanup_views,
                "unsuccessful_collections": unsuccessful_collections,
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

        running_views = d.pop("running_views")

        cleanup_views = d.pop("cleanup_views")

        retried_cleanup_views = d.pop("retried_cleanup_views")

        unsuccessful_collections = d.pop("unsuccessful_collections")

        obs_runtime_build_recovery_counts = cls(
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
            running_views=running_views,
            cleanup_views=cleanup_views,
            retried_cleanup_views=retried_cleanup_views,
            unsuccessful_collections=unsuccessful_collections,
        )

        obs_runtime_build_recovery_counts.additional_properties = d
        return obs_runtime_build_recovery_counts

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
