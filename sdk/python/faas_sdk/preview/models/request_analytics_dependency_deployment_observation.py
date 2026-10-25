from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RequestAnalyticsDependencyDeploymentObservation")


@_attrs_define
class RequestAnalyticsDependencyDeploymentObservation:
    """Sampled latency and error measurements for one route dependency under a single immutable deployment, with an
    advisory comparison to a prior sampled revision when possible.

    """

    deployment_id: str
    """Immutable deployment owning these sampled dependency observations."""
    samples: int
    """Retained dependency span observations attached to this deployment."""
    calls: int
    """Observed span sample count weighted by collapsed request-row count."""
    error_calls: int
    error_rate_pct: float
    """Error share among this deployment's observed weighted calls."""
    p50_ms: int
    p95_ms: int
    p99_ms: int
    exclusive_p95_ms: int
    regression: bool
    """True when p95 rose by at least 25% or error rate rose by at least 2 percentage points."""
    commit_sha: str | Unset = UNSET
    deployment_tag: str | Unset = UNSET
    deployment_created_at: str | Unset = UNSET
    """RFC 3339 creation time used to order comparable revisions."""
    p95_change_pct: float | Unset = UNSET
    """P95 percentage change from the preceding comparable deployment, when its baseline is non-zero."""
    error_rate_change_pct: float | Unset = UNSET
    """Error-rate change in percentage points from the preceding comparable deployment."""
    compared_to: str | Unset = UNSET
    """Tag, commit SHA, or deployment UUID used as the preceding revision baseline."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = self.deployment_id

        samples = self.samples

        calls = self.calls

        error_calls = self.error_calls

        error_rate_pct = self.error_rate_pct

        p50_ms = self.p50_ms

        p95_ms = self.p95_ms

        p99_ms = self.p99_ms

        exclusive_p95_ms = self.exclusive_p95_ms

        regression = self.regression

        commit_sha = self.commit_sha

        deployment_tag = self.deployment_tag

        deployment_created_at = self.deployment_created_at

        p95_change_pct = self.p95_change_pct

        error_rate_change_pct = self.error_rate_change_pct

        compared_to = self.compared_to

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "samples": samples,
                "calls": calls,
                "error_calls": error_calls,
                "error_rate_pct": error_rate_pct,
                "p50_ms": p50_ms,
                "p95_ms": p95_ms,
                "p99_ms": p99_ms,
                "exclusive_p95_ms": exclusive_p95_ms,
                "regression": regression,
            }
        )
        if commit_sha is not UNSET:
            field_dict["commit_sha"] = commit_sha
        if deployment_tag is not UNSET:
            field_dict["deployment_tag"] = deployment_tag
        if deployment_created_at is not UNSET:
            field_dict["deployment_created_at"] = deployment_created_at
        if p95_change_pct is not UNSET:
            field_dict["p95_change_pct"] = p95_change_pct
        if error_rate_change_pct is not UNSET:
            field_dict["error_rate_change_pct"] = error_rate_change_pct
        if compared_to is not UNSET:
            field_dict["compared_to"] = compared_to

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = d.pop("deployment_id")

        samples = d.pop("samples")

        calls = d.pop("calls")

        error_calls = d.pop("error_calls")

        error_rate_pct = d.pop("error_rate_pct")

        p50_ms = d.pop("p50_ms")

        p95_ms = d.pop("p95_ms")

        p99_ms = d.pop("p99_ms")

        exclusive_p95_ms = d.pop("exclusive_p95_ms")

        regression = d.pop("regression")

        commit_sha = d.pop("commit_sha", UNSET)

        deployment_tag = d.pop("deployment_tag", UNSET)

        deployment_created_at = d.pop("deployment_created_at", UNSET)

        p95_change_pct = d.pop("p95_change_pct", UNSET)

        error_rate_change_pct = d.pop("error_rate_change_pct", UNSET)

        compared_to = d.pop("compared_to", UNSET)

        request_analytics_dependency_deployment_observation = cls(
            deployment_id=deployment_id,
            samples=samples,
            calls=calls,
            error_calls=error_calls,
            error_rate_pct=error_rate_pct,
            p50_ms=p50_ms,
            p95_ms=p95_ms,
            p99_ms=p99_ms,
            exclusive_p95_ms=exclusive_p95_ms,
            regression=regression,
            commit_sha=commit_sha,
            deployment_tag=deployment_tag,
            deployment_created_at=deployment_created_at,
            p95_change_pct=p95_change_pct,
            error_rate_change_pct=error_rate_change_pct,
            compared_to=compared_to,
        )

        request_analytics_dependency_deployment_observation.additional_properties = d
        return request_analytics_dependency_deployment_observation

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
