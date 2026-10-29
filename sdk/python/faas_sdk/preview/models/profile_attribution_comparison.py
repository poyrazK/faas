from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.profile_attribution_quality import ProfileAttributionQuality


T = TypeVar("T", bound="ProfileAttributionComparison")


@_attrs_define
class ProfileAttributionComparison:
    """Captured route attribution quality comparison. An absolute labeled-share change of at least 20 percentage points
    suppresses advisory route regression conclusions without changing aggregate results or rollout behavior. Background
    work and traffic changes can also change labeled CPU share.

    """

    available: bool
    substantial_change: bool
    warnings: list[str]
    maximum_change_percentage_points: float = 20.0
    baseline: ProfileAttributionQuality | Unset = UNSET
    """Labeled and unattributed shares of all sampled CPU in the deployment window, even when a route filter is
    selected. Not VM CPU or request instrumentation completeness. Percentages are absent when no CPU was sampled.
   """
    candidate: ProfileAttributionQuality | Unset = UNSET
    """Labeled and unattributed shares of all sampled CPU in the deployment window, even when a route filter is
    selected. Not VM CPU or request instrumentation completeness. Percentages are absent when no CPU was sampled.
   """
    delta_percentage_points: float | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        available = self.available

        substantial_change = self.substantial_change

        maximum_change_percentage_points = self.maximum_change_percentage_points

        warnings = self.warnings

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
                "substantial_change": substantial_change,
                "maximum_change_percentage_points": maximum_change_percentage_points,
                "warnings": warnings,
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
        from ..models.profile_attribution_quality import ProfileAttributionQuality

        d = dict(src_dict)
        available = d.pop("available")

        substantial_change = d.pop("substantial_change")

        maximum_change_percentage_points = d.pop("maximum_change_percentage_points")

        warnings = cast(list[str], d.pop("warnings"))

        _baseline = d.pop("baseline", UNSET)
        baseline: ProfileAttributionQuality | Unset
        if isinstance(_baseline, Unset):
            baseline = UNSET
        else:
            baseline = ProfileAttributionQuality.from_dict(_baseline)

        _candidate = d.pop("candidate", UNSET)
        candidate: ProfileAttributionQuality | Unset
        if isinstance(_candidate, Unset):
            candidate = UNSET
        else:
            candidate = ProfileAttributionQuality.from_dict(_candidate)

        delta_percentage_points = d.pop("delta_percentage_points", UNSET)

        profile_attribution_comparison = cls(
            available=available,
            substantial_change=substantial_change,
            maximum_change_percentage_points=maximum_change_percentage_points,
            warnings=warnings,
            baseline=baseline,
            candidate=candidate,
            delta_percentage_points=delta_percentage_points,
        )

        profile_attribution_comparison.additional_properties = d
        return profile_attribution_comparison

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
