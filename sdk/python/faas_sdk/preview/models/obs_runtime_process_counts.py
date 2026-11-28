from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObsRuntimeProcessCounts")


@_attrs_define
class ObsRuntimeProcessCounts:
    """Sample states partition current inspected targets. Comparison counts cover fresh samples; removed targets belong to
    the prior observation and overlap no current partition.

    """

    fresh: int
    missing: int
    stale: int
    conflicting: int
    unavailable: int
    compared: int
    changed: int
    unchanged: int
    same_revision_changes: int
    different_revision_changes: int
    unknown_revision_changes: int
    new_targets: int
    removed_targets: int
    repeated_samples: int
    comparison_unavailable: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        fresh = self.fresh

        missing = self.missing

        stale = self.stale

        conflicting = self.conflicting

        unavailable = self.unavailable

        compared = self.compared

        changed = self.changed

        unchanged = self.unchanged

        same_revision_changes = self.same_revision_changes

        different_revision_changes = self.different_revision_changes

        unknown_revision_changes = self.unknown_revision_changes

        new_targets = self.new_targets

        removed_targets = self.removed_targets

        repeated_samples = self.repeated_samples

        comparison_unavailable = self.comparison_unavailable

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "fresh": fresh,
                "missing": missing,
                "stale": stale,
                "conflicting": conflicting,
                "unavailable": unavailable,
                "compared": compared,
                "changed": changed,
                "unchanged": unchanged,
                "same_revision_changes": same_revision_changes,
                "different_revision_changes": different_revision_changes,
                "unknown_revision_changes": unknown_revision_changes,
                "new_targets": new_targets,
                "removed_targets": removed_targets,
                "repeated_samples": repeated_samples,
                "comparison_unavailable": comparison_unavailable,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        fresh = d.pop("fresh")

        missing = d.pop("missing")

        stale = d.pop("stale")

        conflicting = d.pop("conflicting")

        unavailable = d.pop("unavailable")

        compared = d.pop("compared")

        changed = d.pop("changed")

        unchanged = d.pop("unchanged")

        same_revision_changes = d.pop("same_revision_changes")

        different_revision_changes = d.pop("different_revision_changes")

        unknown_revision_changes = d.pop("unknown_revision_changes")

        new_targets = d.pop("new_targets")

        removed_targets = d.pop("removed_targets")

        repeated_samples = d.pop("repeated_samples")

        comparison_unavailable = d.pop("comparison_unavailable")

        obs_runtime_process_counts = cls(
            fresh=fresh,
            missing=missing,
            stale=stale,
            conflicting=conflicting,
            unavailable=unavailable,
            compared=compared,
            changed=changed,
            unchanged=unchanged,
            same_revision_changes=same_revision_changes,
            different_revision_changes=different_revision_changes,
            unknown_revision_changes=unknown_revision_changes,
            new_targets=new_targets,
            removed_targets=removed_targets,
            repeated_samples=repeated_samples,
            comparison_unavailable=comparison_unavailable,
        )

        obs_runtime_process_counts.additional_properties = d
        return obs_runtime_process_counts

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
