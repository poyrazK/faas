from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.profile_regression_metric_cpu_per_request import ProfileRegressionMetricCpuPerRequest


T = TypeVar("T", bound="ProfileRegressionMetric")


@_attrs_define
class ProfileRegressionMetric:
    """Observed sampled CPU/s comparison, optionally with CPU seconds per weighted observed request. exceeds_threshold and
    relative_increase_percent use the metric selected by options. The relative percentage is absent when that metric's
    baseline is zero.

    """

    baseline_cpu_per_second: float
    candidate_cpu_per_second: float
    delta_cpu_per_second: float
    exceeds_threshold: bool
    relative_increase_percent: float | Unset = UNSET
    cpu_per_request: ProfileRegressionMetricCpuPerRequest | Unset = UNSET
    """Present when retained request telemetry has observations for both deployment windows. Counts are weighted
    collapsed rows with minute-bucket timestamps; boundary counts can be approximate and telemetry can be
    incomplete."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        baseline_cpu_per_second = self.baseline_cpu_per_second

        candidate_cpu_per_second = self.candidate_cpu_per_second

        delta_cpu_per_second = self.delta_cpu_per_second

        exceeds_threshold = self.exceeds_threshold

        relative_increase_percent = self.relative_increase_percent

        cpu_per_request: dict[str, Any] | Unset = UNSET
        if not isinstance(self.cpu_per_request, Unset):
            cpu_per_request = self.cpu_per_request.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "baseline_cpu_per_second": baseline_cpu_per_second,
                "candidate_cpu_per_second": candidate_cpu_per_second,
                "delta_cpu_per_second": delta_cpu_per_second,
                "exceeds_threshold": exceeds_threshold,
            }
        )
        if relative_increase_percent is not UNSET:
            field_dict["relative_increase_percent"] = relative_increase_percent
        if cpu_per_request is not UNSET:
            field_dict["cpu_per_request"] = cpu_per_request

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_regression_metric_cpu_per_request import ProfileRegressionMetricCpuPerRequest

        d = dict(src_dict)
        baseline_cpu_per_second = d.pop("baseline_cpu_per_second")

        candidate_cpu_per_second = d.pop("candidate_cpu_per_second")

        delta_cpu_per_second = d.pop("delta_cpu_per_second")

        exceeds_threshold = d.pop("exceeds_threshold")

        relative_increase_percent = d.pop("relative_increase_percent", UNSET)

        _cpu_per_request = d.pop("cpu_per_request", UNSET)
        cpu_per_request: ProfileRegressionMetricCpuPerRequest | Unset
        if isinstance(_cpu_per_request, Unset):
            cpu_per_request = UNSET
        else:
            cpu_per_request = ProfileRegressionMetricCpuPerRequest.from_dict(_cpu_per_request)

        profile_regression_metric = cls(
            baseline_cpu_per_second=baseline_cpu_per_second,
            candidate_cpu_per_second=candidate_cpu_per_second,
            delta_cpu_per_second=delta_cpu_per_second,
            exceeds_threshold=exceeds_threshold,
            relative_increase_percent=relative_increase_percent,
            cpu_per_request=cpu_per_request,
        )

        profile_regression_metric.additional_properties = d
        return profile_regression_metric

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
