from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RequestAnalyticsRouteDeploymentObservation")


@_attrs_define
class RequestAnalyticsRouteDeploymentObservation:
    """One route/method's bounded request and estimated-compute share for an immutable deployment. CPU comparisons are
    advisory, require at least 20 measured requests on both revisions, and remain sensitive to traffic mix.

    """

    deployment_id: str
    """Stable UUID identifying the immutable revision that served this route."""
    requests: int
    request_share_pct: float
    """This route/deployment cell's share of all observed app requests in the window."""
    estimated_compute_cost_millicents: int
    """Estimated raw RAM-hour value allocated to this route/deployment cell by request share; not an invoice
    amount."""
    guest_cpu_measured_requests: int
    guest_cpu_regression: bool
    """True when measured guest CPU/request rose by at least 25% from the preceding comparable revision."""
    commit_sha: str | Unset = UNSET
    deployment_tag: str | Unset = UNSET
    deployment_created_at: str | Unset = UNSET
    """RFC 3339 timestamp used to order same-route CPU comparisons."""
    guest_cpu_avg_ms: int | Unset = UNSET
    """Request-weighted mean measured guest CPU time for this route under this deployment; supported Linux one-shot
    runtimes only."""
    guest_cpu_change_pct: float | Unset = UNSET
    """Change in measured mean guest CPU/request from the preceding comparable deployment for this same
    route/method."""
    guest_cpu_compared_to: str | Unset = UNSET
    """Tag, commit SHA, or deployment UUID of the preceding comparable revision."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = self.deployment_id

        requests = self.requests

        request_share_pct = self.request_share_pct

        estimated_compute_cost_millicents = self.estimated_compute_cost_millicents

        guest_cpu_measured_requests = self.guest_cpu_measured_requests

        guest_cpu_regression = self.guest_cpu_regression

        commit_sha = self.commit_sha

        deployment_tag = self.deployment_tag

        deployment_created_at = self.deployment_created_at

        guest_cpu_avg_ms = self.guest_cpu_avg_ms

        guest_cpu_change_pct = self.guest_cpu_change_pct

        guest_cpu_compared_to = self.guest_cpu_compared_to

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "requests": requests,
                "request_share_pct": request_share_pct,
                "estimated_compute_cost_millicents": estimated_compute_cost_millicents,
                "guest_cpu_measured_requests": guest_cpu_measured_requests,
                "guest_cpu_regression": guest_cpu_regression,
            }
        )
        if commit_sha is not UNSET:
            field_dict["commit_sha"] = commit_sha
        if deployment_tag is not UNSET:
            field_dict["deployment_tag"] = deployment_tag
        if deployment_created_at is not UNSET:
            field_dict["deployment_created_at"] = deployment_created_at
        if guest_cpu_avg_ms is not UNSET:
            field_dict["guest_cpu_avg_ms"] = guest_cpu_avg_ms
        if guest_cpu_change_pct is not UNSET:
            field_dict["guest_cpu_change_pct"] = guest_cpu_change_pct
        if guest_cpu_compared_to is not UNSET:
            field_dict["guest_cpu_compared_to"] = guest_cpu_compared_to

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = d.pop("deployment_id")

        requests = d.pop("requests")

        request_share_pct = d.pop("request_share_pct")

        estimated_compute_cost_millicents = d.pop("estimated_compute_cost_millicents")

        guest_cpu_measured_requests = d.pop("guest_cpu_measured_requests")

        guest_cpu_regression = d.pop("guest_cpu_regression")

        commit_sha = d.pop("commit_sha", UNSET)

        deployment_tag = d.pop("deployment_tag", UNSET)

        deployment_created_at = d.pop("deployment_created_at", UNSET)

        guest_cpu_avg_ms = d.pop("guest_cpu_avg_ms", UNSET)

        guest_cpu_change_pct = d.pop("guest_cpu_change_pct", UNSET)

        guest_cpu_compared_to = d.pop("guest_cpu_compared_to", UNSET)

        request_analytics_route_deployment_observation = cls(
            deployment_id=deployment_id,
            requests=requests,
            request_share_pct=request_share_pct,
            estimated_compute_cost_millicents=estimated_compute_cost_millicents,
            guest_cpu_measured_requests=guest_cpu_measured_requests,
            guest_cpu_regression=guest_cpu_regression,
            commit_sha=commit_sha,
            deployment_tag=deployment_tag,
            deployment_created_at=deployment_created_at,
            guest_cpu_avg_ms=guest_cpu_avg_ms,
            guest_cpu_change_pct=guest_cpu_change_pct,
            guest_cpu_compared_to=guest_cpu_compared_to,
        )

        request_analytics_route_deployment_observation.additional_properties = d
        return request_analytics_route_deployment_observation

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
