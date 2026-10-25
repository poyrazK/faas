from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ProfileRouteLabelCoverage")


@_attrs_define
class ProfileRouteLabelCoverage:
    """Application-reported labeled request entries in retained whole captures divided by weighted observed requests in the
    deployment window. Boundary captures are excluded without extrapolation. This is not proof of full request
    instrumentation. Counts above observed traffic cannot be reconciled.

    """

    available: bool
    reason: str
    captured_profiles: int
    boundary_profiles: int
    labeled_requests: int | Unset = UNSET
    observed_requests: int | Unset = UNSET
    percent: float | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        available = self.available

        reason = self.reason

        captured_profiles = self.captured_profiles

        boundary_profiles = self.boundary_profiles

        labeled_requests = self.labeled_requests

        observed_requests = self.observed_requests

        percent = self.percent

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "available": available,
                "reason": reason,
                "captured_profiles": captured_profiles,
                "boundary_profiles": boundary_profiles,
            }
        )
        if labeled_requests is not UNSET:
            field_dict["labeled_requests"] = labeled_requests
        if observed_requests is not UNSET:
            field_dict["observed_requests"] = observed_requests
        if percent is not UNSET:
            field_dict["percent"] = percent

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        available = d.pop("available")

        reason = d.pop("reason")

        captured_profiles = d.pop("captured_profiles")

        boundary_profiles = d.pop("boundary_profiles")

        labeled_requests = d.pop("labeled_requests", UNSET)

        observed_requests = d.pop("observed_requests", UNSET)

        percent = d.pop("percent", UNSET)

        profile_route_label_coverage = cls(
            available=available,
            reason=reason,
            captured_profiles=captured_profiles,
            boundary_profiles=boundary_profiles,
            labeled_requests=labeled_requests,
            observed_requests=observed_requests,
            percent=percent,
        )

        profile_route_label_coverage.additional_properties = d
        return profile_route_label_coverage

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
