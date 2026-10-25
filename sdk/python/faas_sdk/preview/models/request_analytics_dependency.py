from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.request_analytics_dependency_deployment_observation import (
        RequestAnalyticsDependencyDeploymentObservation,
    )


T = TypeVar("T", bound="RequestAnalyticsDependency")


@_attrs_define
class RequestAnalyticsDependency:
    """Sampled aggregate of platform-classified dependency spans observed under one route. Exclusive duration subtracts
    overlapping direct child spans; it approximates dependency-owned wait and is not additive request latency. Calls are
    weighted by collapsed request count and are not a complete span call count. Deployment observations compare the same
    route and dependency across immutable revisions when chronology and sample size permit.

    """

    type_: str
    """Allowlisted dependency category."""
    name: str
    """Redacted span name."""
    samples: int
    """Number of retained span observations."""
    calls: int
    """Observed span sample count weighted by the collapsed request-row count; not a full call count."""
    error_calls: int
    error_rate_pct: float
    """Error calls divided by observed weighted calls."""
    p50_ms: int
    """Weighted p50 inclusive span duration."""
    p95_ms: int
    """Weighted p95 inclusive span duration."""
    p99_ms: int
    """Weighted p99 inclusive span duration."""
    exclusive_p95_ms: int
    """Weighted p95 span duration excluding overlapping direct child spans."""
    kind: str | Unset = UNSET
    """Allowlisted dependency kind when available."""
    deployment_observations: list[RequestAnalyticsDependencyDeploymentObservation] | Unset = UNSET
    """At most the five newest deployment observations for this route/dependency pair; comparisons are advisory and
    require at least 20 retained span observations on each deployment."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        type_ = self.type_

        name = self.name

        samples = self.samples

        calls = self.calls

        error_calls = self.error_calls

        error_rate_pct = self.error_rate_pct

        p50_ms = self.p50_ms

        p95_ms = self.p95_ms

        p99_ms = self.p99_ms

        exclusive_p95_ms = self.exclusive_p95_ms

        kind = self.kind

        deployment_observations: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.deployment_observations, Unset):
            deployment_observations = []
            for deployment_observations_item_data in self.deployment_observations:
                deployment_observations_item = deployment_observations_item_data.to_dict()
                deployment_observations.append(deployment_observations_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "type": type_,
                "name": name,
                "samples": samples,
                "calls": calls,
                "error_calls": error_calls,
                "error_rate_pct": error_rate_pct,
                "p50_ms": p50_ms,
                "p95_ms": p95_ms,
                "p99_ms": p99_ms,
                "exclusive_p95_ms": exclusive_p95_ms,
            }
        )
        if kind is not UNSET:
            field_dict["kind"] = kind
        if deployment_observations is not UNSET:
            field_dict["deployment_observations"] = deployment_observations

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.request_analytics_dependency_deployment_observation import (
            RequestAnalyticsDependencyDeploymentObservation,
        )

        d = dict(src_dict)
        type_ = d.pop("type")

        name = d.pop("name")

        samples = d.pop("samples")

        calls = d.pop("calls")

        error_calls = d.pop("error_calls")

        error_rate_pct = d.pop("error_rate_pct")

        p50_ms = d.pop("p50_ms")

        p95_ms = d.pop("p95_ms")

        p99_ms = d.pop("p99_ms")

        exclusive_p95_ms = d.pop("exclusive_p95_ms")

        kind = d.pop("kind", UNSET)

        _deployment_observations = d.pop("deployment_observations", UNSET)
        deployment_observations: list[RequestAnalyticsDependencyDeploymentObservation] | Unset = UNSET
        if _deployment_observations is not UNSET:
            deployment_observations = []
            for deployment_observations_item_data in _deployment_observations:
                deployment_observations_item = RequestAnalyticsDependencyDeploymentObservation.from_dict(
                    deployment_observations_item_data
                )

                deployment_observations.append(deployment_observations_item)

        request_analytics_dependency = cls(
            type_=type_,
            name=name,
            samples=samples,
            calls=calls,
            error_calls=error_calls,
            error_rate_pct=error_rate_pct,
            p50_ms=p50_ms,
            p95_ms=p95_ms,
            p99_ms=p99_ms,
            exclusive_p95_ms=exclusive_p95_ms,
            kind=kind,
            deployment_observations=deployment_observations,
        )

        request_analytics_dependency.additional_properties = d
        return request_analytics_dependency

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
