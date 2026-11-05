from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_health_evaluation_policy_version import (
    RouteHealthEvaluationPolicyVersion,
    check_route_health_evaluation_policy_version,
)

T = TypeVar("T", bound="RouteHealthEvaluationPolicy")


@_attrs_define
class RouteHealthEvaluationPolicy:
    """Versioned thresholds captured with the decision; never inferred from current settings."""

    version: RouteHealthEvaluationPolicyVersion
    windows: int
    window_seconds: int
    ingestion_lag_seconds: int
    minimum_requests: int
    minimum_errors: int
    error_rate_floor: float
    """Minimum candidate 5xx rate as a fraction."""
    error_rate_delta: float
    """Minimum additional 5xx rate as a fraction."""
    error_rate_factor: float
    minimum_latency_requests: int
    latency_quantile: float
    latency_factor: float
    latency_delta_ms: float
    comparison_epsilon: float
    """Floating point tolerance used in comparisons."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        windows = self.windows

        window_seconds = self.window_seconds

        ingestion_lag_seconds = self.ingestion_lag_seconds

        minimum_requests = self.minimum_requests

        minimum_errors = self.minimum_errors

        error_rate_floor = self.error_rate_floor

        error_rate_delta = self.error_rate_delta

        error_rate_factor = self.error_rate_factor

        minimum_latency_requests = self.minimum_latency_requests

        latency_quantile = self.latency_quantile

        latency_factor = self.latency_factor

        latency_delta_ms = self.latency_delta_ms

        comparison_epsilon = self.comparison_epsilon

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "windows": windows,
                "window_seconds": window_seconds,
                "ingestion_lag_seconds": ingestion_lag_seconds,
                "minimum_requests": minimum_requests,
                "minimum_errors": minimum_errors,
                "error_rate_floor": error_rate_floor,
                "error_rate_delta": error_rate_delta,
                "error_rate_factor": error_rate_factor,
                "minimum_latency_requests": minimum_latency_requests,
                "latency_quantile": latency_quantile,
                "latency_factor": latency_factor,
                "latency_delta_ms": latency_delta_ms,
                "comparison_epsilon": comparison_epsilon,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = check_route_health_evaluation_policy_version(d.pop("version"))

        windows = d.pop("windows")

        window_seconds = d.pop("window_seconds")

        ingestion_lag_seconds = d.pop("ingestion_lag_seconds")

        minimum_requests = d.pop("minimum_requests")

        minimum_errors = d.pop("minimum_errors")

        error_rate_floor = d.pop("error_rate_floor")

        error_rate_delta = d.pop("error_rate_delta")

        error_rate_factor = d.pop("error_rate_factor")

        minimum_latency_requests = d.pop("minimum_latency_requests")

        latency_quantile = d.pop("latency_quantile")

        latency_factor = d.pop("latency_factor")

        latency_delta_ms = d.pop("latency_delta_ms")

        comparison_epsilon = d.pop("comparison_epsilon")

        route_health_evaluation_policy = cls(
            version=version,
            windows=windows,
            window_seconds=window_seconds,
            ingestion_lag_seconds=ingestion_lag_seconds,
            minimum_requests=minimum_requests,
            minimum_errors=minimum_errors,
            error_rate_floor=error_rate_floor,
            error_rate_delta=error_rate_delta,
            error_rate_factor=error_rate_factor,
            minimum_latency_requests=minimum_latency_requests,
            latency_quantile=latency_quantile,
            latency_factor=latency_factor,
            latency_delta_ms=latency_delta_ms,
            comparison_epsilon=comparison_epsilon,
        )

        route_health_evaluation_policy.additional_properties = d
        return route_health_evaluation_policy

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
