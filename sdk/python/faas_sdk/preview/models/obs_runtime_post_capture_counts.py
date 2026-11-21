from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObsRuntimePostCaptureCounts")


@_attrs_define
class ObsRuntimePostCaptureCounts:
    """Complete/missing/stale/conflicting/unavailable partition replica observations. Other counts overlap the partition
    and count replica views, not deployments. Nonempty means any reported durable class evidence; empty requires all
    eight classes observed empty. Complete component evidence can contain unknown ages. Conflicting means inconsistent
    source series, not disagreement between replicas.

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

        obs_runtime_post_capture_counts = cls(
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
        )

        obs_runtime_post_capture_counts.additional_properties = d
        return obs_runtime_post_capture_counts

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
