from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.profile_route_label_coverage import ProfileRouteLabelCoverage


T = TypeVar("T", bound="ProfileRouteLabelComparison")


@_attrs_define
class ProfileRouteLabelComparison:
    """Advisory route CPU/request requires reconciled labeling shares of at least 80 percent in both windows and a change
    below 20 percentage points in either direction. Missing reports or failed reconciliation produces insufficient data.

    """

    available: bool
    consistent: bool
    reason: str
    minimum_percent: float = 80.0
    maximum_change_percentage_points: float = 20.0
    baseline: ProfileRouteLabelCoverage | Unset = UNSET
    """Application-reported labeled request entries in retained whole captures divided by weighted observed
    requests in the deployment window. Boundary captures are excluded without extrapolation. This is not proof of
    full request instrumentation. Counts above observed traffic cannot be reconciled."""
    candidate: ProfileRouteLabelCoverage | Unset = UNSET
    """Application-reported labeled request entries in retained whole captures divided by weighted observed
    requests in the deployment window. Boundary captures are excluded without extrapolation. This is not proof of
    full request instrumentation. Counts above observed traffic cannot be reconciled."""
    delta_percentage_points: float | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        available = self.available

        consistent = self.consistent

        reason = self.reason

        minimum_percent = self.minimum_percent

        maximum_change_percentage_points = self.maximum_change_percentage_points

        baseline: dict[str, Any] | Unset = UNSET
        if not isinstance(self.baseline, Unset):
            baseline = self.baseline.to_dict()

        candidate: dict[str, Any] | Unset = UNSET
        if not isinstance(self.candidate, Unset):
            candidate = self.candidate.to_dict()

        delta_percentage_points = self.delta_percentage_points

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "available": available,
                "consistent": consistent,
                "reason": reason,
                "minimum_percent": minimum_percent,
                "maximum_change_percentage_points": maximum_change_percentage_points,
            }
        )
        if baseline is not UNSET:
            field_dict["baseline"] = baseline
        if candidate is not UNSET:
            field_dict["candidate"] = candidate
        if delta_percentage_points is not UNSET:
            field_dict["delta_percentage_points"] = delta_percentage_points

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_route_label_coverage import ProfileRouteLabelCoverage

        d = dict(src_dict)
        available = d.pop("available")

        consistent = d.pop("consistent")

        reason = d.pop("reason")

        minimum_percent = d.pop("minimum_percent")

        maximum_change_percentage_points = d.pop("maximum_change_percentage_points")

        _baseline = d.pop("baseline", UNSET)
        baseline: ProfileRouteLabelCoverage | Unset
        if isinstance(_baseline, Unset):
            baseline = UNSET
        else:
            baseline = ProfileRouteLabelCoverage.from_dict(_baseline)

        _candidate = d.pop("candidate", UNSET)
        candidate: ProfileRouteLabelCoverage | Unset
        if isinstance(_candidate, Unset):
            candidate = UNSET
        else:
            candidate = ProfileRouteLabelCoverage.from_dict(_candidate)

        delta_percentage_points = d.pop("delta_percentage_points", UNSET)

        profile_route_label_comparison = cls(
            available=available,
            consistent=consistent,
            reason=reason,
            minimum_percent=minimum_percent,
            maximum_change_percentage_points=maximum_change_percentage_points,
            baseline=baseline,
            candidate=candidate,
            delta_percentage_points=delta_percentage_points,
        )

        profile_route_label_comparison.additional_properties = d
        return profile_route_label_comparison

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
