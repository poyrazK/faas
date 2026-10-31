from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.profile_regression_options_metric import (
    ProfileRegressionOptionsMetric,
    check_profile_regression_options_metric,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ProfileRegressionOptions")


@_attrs_define
class ProfileRegressionOptions:
    """Threshold policy for sampled CPU per wall-clock second or per weighted observed request. Both relative and selected-
    metric absolute increase thresholds must be met. CPU-per-request mode also requires retained request telemetry and
    its configured minimum request count in both deployment windows; absent or sparse counts are inconclusive. Request
    telemetry rows use minute-bucket timestamps, so counts near window boundaries can be approximate and may be
    incomplete. Capture requirements apply separately to each profile window.

    """

    relative_increase_percent: float = 20.0
    absolute_increase_cpu_per_second: float = 0.01
    """Absolute increase threshold for cpu_per_second mode."""
    minimum_profiles: int = 3
    minimum_coverage_ratio: float = 0.8
    routes: list[str] | Unset = UNSET
    """Optional advisory route CPU/request checks using the relative and CPU/request thresholds and minimum
    requests, regardless of the aggregate metric. Never gates rollout."""
    metric: ProfileRegressionOptionsMetric | Unset = "cpu_per_second"
    absolute_increase_cpu_seconds_per_request: float | Unset = 0.0001
    """Required when metric is cpu_per_request."""
    minimum_requests: int | Unset = 20
    """Required when metric is cpu_per_request; applied to each deployment window."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        relative_increase_percent = self.relative_increase_percent

        absolute_increase_cpu_per_second = self.absolute_increase_cpu_per_second

        minimum_profiles = self.minimum_profiles

        minimum_coverage_ratio = self.minimum_coverage_ratio

        routes: list[str] | Unset = UNSET
        if not isinstance(self.routes, Unset):
            routes = self.routes

        metric: str | Unset = UNSET
        if not isinstance(self.metric, Unset):
            metric = self.metric

        absolute_increase_cpu_seconds_per_request = self.absolute_increase_cpu_seconds_per_request

        minimum_requests = self.minimum_requests

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "relative_increase_percent": relative_increase_percent,
                "absolute_increase_cpu_per_second": absolute_increase_cpu_per_second,
                "minimum_profiles": minimum_profiles,
                "minimum_coverage_ratio": minimum_coverage_ratio,
            }
        )
        if routes is not UNSET:
            field_dict["routes"] = routes
        if metric is not UNSET:
            field_dict["metric"] = metric
        if absolute_increase_cpu_seconds_per_request is not UNSET:
            field_dict["absolute_increase_cpu_seconds_per_request"] = absolute_increase_cpu_seconds_per_request
        if minimum_requests is not UNSET:
            field_dict["minimum_requests"] = minimum_requests

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        relative_increase_percent = d.pop("relative_increase_percent")

        absolute_increase_cpu_per_second = d.pop("absolute_increase_cpu_per_second")

        minimum_profiles = d.pop("minimum_profiles")

        minimum_coverage_ratio = d.pop("minimum_coverage_ratio")

        routes = cast(list[str], d.pop("routes", UNSET))

        _metric = d.pop("metric", UNSET)
        metric: ProfileRegressionOptionsMetric | Unset
        if isinstance(_metric, Unset):
            metric = UNSET
        else:
            metric = check_profile_regression_options_metric(_metric)

        absolute_increase_cpu_seconds_per_request = d.pop("absolute_increase_cpu_seconds_per_request", UNSET)

        minimum_requests = d.pop("minimum_requests", UNSET)

        profile_regression_options = cls(
            relative_increase_percent=relative_increase_percent,
            absolute_increase_cpu_per_second=absolute_increase_cpu_per_second,
            minimum_profiles=minimum_profiles,
            minimum_coverage_ratio=minimum_coverage_ratio,
            routes=routes,
            metric=metric,
            absolute_increase_cpu_seconds_per_request=absolute_increase_cpu_seconds_per_request,
            minimum_requests=minimum_requests,
        )

        profile_regression_options.additional_properties = d
        return profile_regression_options

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
