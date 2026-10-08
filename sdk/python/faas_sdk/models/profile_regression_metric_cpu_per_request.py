from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ProfileRegressionMetricCpuPerRequest")


@_attrs_define
class ProfileRegressionMetricCpuPerRequest:
    """Present when retained request telemetry has observations for both deployment windows. Counts are weighted collapsed
    rows with minute-bucket timestamps; boundary counts can be approximate and telemetry can be incomplete.

    """

    baseline_cpu_seconds_per_request: float
    candidate_cpu_seconds_per_request: float
    delta_cpu_seconds_per_request: float
    exceeds_threshold: bool
    relative_increase_percent: float | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        baseline_cpu_seconds_per_request = self.baseline_cpu_seconds_per_request

        candidate_cpu_seconds_per_request = self.candidate_cpu_seconds_per_request

        delta_cpu_seconds_per_request = self.delta_cpu_seconds_per_request

        exceeds_threshold = self.exceeds_threshold

        relative_increase_percent = self.relative_increase_percent

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "baseline_cpu_seconds_per_request": baseline_cpu_seconds_per_request,
                "candidate_cpu_seconds_per_request": candidate_cpu_seconds_per_request,
                "delta_cpu_seconds_per_request": delta_cpu_seconds_per_request,
                "exceeds_threshold": exceeds_threshold,
            }
        )
        if relative_increase_percent is not UNSET:
            field_dict["relative_increase_percent"] = relative_increase_percent

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        baseline_cpu_seconds_per_request = d.pop("baseline_cpu_seconds_per_request")

        candidate_cpu_seconds_per_request = d.pop("candidate_cpu_seconds_per_request")

        delta_cpu_seconds_per_request = d.pop("delta_cpu_seconds_per_request")

        exceeds_threshold = d.pop("exceeds_threshold")

        relative_increase_percent = d.pop("relative_increase_percent", UNSET)

        profile_regression_metric_cpu_per_request = cls(
            baseline_cpu_seconds_per_request=baseline_cpu_seconds_per_request,
            candidate_cpu_seconds_per_request=candidate_cpu_seconds_per_request,
            delta_cpu_seconds_per_request=delta_cpu_seconds_per_request,
            exceeds_threshold=exceeds_threshold,
            relative_increase_percent=relative_increase_percent,
        )

        profile_regression_metric_cpu_per_request.additional_properties = d
        return profile_regression_metric_cpu_per_request

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
