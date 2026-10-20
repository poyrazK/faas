from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_health_report_client_error_status import (
    RouteHealthReportClientErrorStatus,
    check_route_health_report_client_error_status,
)
from ..models.route_health_report_coverage import RouteHealthReportCoverage, check_route_health_report_coverage
from ..models.route_health_report_mode import RouteHealthReportMode, check_route_health_report_mode
from ..models.route_health_report_on_regression import (
    RouteHealthReportOnRegression,
    check_route_health_report_on_regression,
)
from ..models.route_health_report_status import RouteHealthReportStatus, check_route_health_report_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.canary_profile_signal import CanaryProfileSignal
    from ..models.route_customer_health_report import RouteCustomerHealthReport
    from ..models.route_health_finding import RouteHealthFinding


T = TypeVar("T", bound="RouteHealthReport")


@_attrs_define
class RouteHealthReport:
    """Current observed-only critical-route health comparison with candidate, predecessor, policy, and telemetry
    provenance. When the app's automatic profile policy is enabled, profile_signal adds an ephemeral report-only
    comparison for an active canary; it never affects canary advancement or rollback and is not persisted.

    """

    app_id: UUID
    deployment_id: UUID
    candidate_commit_sha: str
    stable_deployment_id: str
    """Empty when no unique serving predecessor exists."""
    stable_commit_sha: str
    canary_step: int
    mode: RouteHealthReportMode
    revision: int
    checked_at: datetime.datetime
    coverage: RouteHealthReportCoverage
    status: RouteHealthReportStatus
    reason: str
    minimum_requests: int
    routes: list[RouteHealthFinding]
    client_error_status: RouteHealthReportClientErrorStatus | Unset = UNSET
    """Live advisory summary of selected status-code comparisons. Independent of the status used for rollout
    decisions. Omitted when no codes are selected."""
    client_error_reason: str | Unset = UNSET
    customers: RouteCustomerHealthReport | Unset = UNSET
    """Advisory live comparisons in the same repeatable-read snapshot as aggregate health. Reuses sample minima and
    consecutive-window error and selected latency checks per identity, with watched client responses included in the
    advisory customer summary. A confirmed observed regression takes precedence; empty, sparse, capped,
    unattributed, unresolved or unavailable evidence prevents a healthy summary. Healthy means the retained observed
    cohort comparisons passed, not proof of complete capture or a statistical SLO. Request-time tenant IDs never
    follow current consumer links. Revoked consumers remain eligible historical observations. No identities enter
    decisions, history, audits or webhooks."""
    on_regression: RouteHealthReportOnRegression | Unset = UNSET
    """Recovery policy used for this observation. Abort permits worker recovery on confirmed critical-route errors
    in enforce mode; reading the report never performs that action."""
    observation_anchor: datetime.datetime | Unset = UNSET
    """Latest stage or configuration timestamp that both windows must follow."""
    minimum_latency_requests: int | Unset = UNSET
    """Present when any route has a latency check selected. Applies to each deployment per route per window."""
    profile_signal: CanaryProfileSignal | Unset = UNSET
    """Latest retained sampled CPU comparison for a deployment's canary stages, produced by a background worker
    using the app's enabled automatic profile policy and equal fixed windows. The stage and policy revision identify
    the assessment. Route-health reads return the saved result and never query profile storage. Checks are advisory
    unless canary_gate is explicitly configured. Gate evidence requires consecutive distinct qualified route
    windows; timeout behavior is configured explicitly. Completed assessments are retained for 30 days."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        deployment_id = str(self.deployment_id)

        candidate_commit_sha = self.candidate_commit_sha

        stable_deployment_id = self.stable_deployment_id

        stable_commit_sha = self.stable_commit_sha

        canary_step = self.canary_step

        mode: str = self.mode

        revision = self.revision

        checked_at = self.checked_at.isoformat()

        coverage: str = self.coverage

        status: str = self.status

        reason = self.reason

        minimum_requests = self.minimum_requests

        routes = []
        for routes_item_data in self.routes:
            routes_item = routes_item_data.to_dict()
            routes.append(routes_item)

        client_error_status: str | Unset = UNSET
        if not isinstance(self.client_error_status, Unset):
            client_error_status = self.client_error_status

        client_error_reason = self.client_error_reason

        customers: dict[str, Any] | Unset = UNSET
        if not isinstance(self.customers, Unset):
            customers = self.customers.to_dict()

        on_regression: str | Unset = UNSET
        if not isinstance(self.on_regression, Unset):
            on_regression = self.on_regression

        observation_anchor: str | Unset = UNSET
        if not isinstance(self.observation_anchor, Unset):
            observation_anchor = self.observation_anchor.isoformat()

        minimum_latency_requests = self.minimum_latency_requests

        profile_signal: dict[str, Any] | Unset = UNSET
        if not isinstance(self.profile_signal, Unset):
            profile_signal = self.profile_signal.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "deployment_id": deployment_id,
                "candidate_commit_sha": candidate_commit_sha,
                "stable_deployment_id": stable_deployment_id,
                "stable_commit_sha": stable_commit_sha,
                "canary_step": canary_step,
                "mode": mode,
                "revision": revision,
                "checked_at": checked_at,
                "coverage": coverage,
                "status": status,
                "reason": reason,
                "minimum_requests": minimum_requests,
                "routes": routes,
            }
        )
        if client_error_status is not UNSET:
            field_dict["client_error_status"] = client_error_status
        if client_error_reason is not UNSET:
            field_dict["client_error_reason"] = client_error_reason
        if customers is not UNSET:
            field_dict["customers"] = customers
        if on_regression is not UNSET:
            field_dict["on_regression"] = on_regression
        if observation_anchor is not UNSET:
            field_dict["observation_anchor"] = observation_anchor
        if minimum_latency_requests is not UNSET:
            field_dict["minimum_latency_requests"] = minimum_latency_requests
        if profile_signal is not UNSET:
            field_dict["profile_signal"] = profile_signal

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.canary_profile_signal import CanaryProfileSignal
        from ..models.route_customer_health_report import RouteCustomerHealthReport
        from ..models.route_health_finding import RouteHealthFinding

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        deployment_id = UUID(d.pop("deployment_id"))

        candidate_commit_sha = d.pop("candidate_commit_sha")

        stable_deployment_id = d.pop("stable_deployment_id")

        stable_commit_sha = d.pop("stable_commit_sha")

        canary_step = d.pop("canary_step")

        mode = check_route_health_report_mode(d.pop("mode"))

        revision = d.pop("revision")

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        coverage = check_route_health_report_coverage(d.pop("coverage"))

        status = check_route_health_report_status(d.pop("status"))

        reason = d.pop("reason")

        minimum_requests = d.pop("minimum_requests")

        routes = []
        _routes = d.pop("routes")
        for routes_item_data in _routes:
            routes_item = RouteHealthFinding.from_dict(routes_item_data)

            routes.append(routes_item)

        _client_error_status = d.pop("client_error_status", UNSET)
        client_error_status: RouteHealthReportClientErrorStatus | Unset
        if isinstance(_client_error_status, Unset):
            client_error_status = UNSET
        else:
            client_error_status = check_route_health_report_client_error_status(_client_error_status)

        client_error_reason = d.pop("client_error_reason", UNSET)

        _customers = d.pop("customers", UNSET)
        customers: RouteCustomerHealthReport | Unset
        if isinstance(_customers, Unset):
            customers = UNSET
        else:
            customers = RouteCustomerHealthReport.from_dict(_customers)

        _on_regression = d.pop("on_regression", UNSET)
        on_regression: RouteHealthReportOnRegression | Unset
        if isinstance(_on_regression, Unset):
            on_regression = UNSET
        else:
            on_regression = check_route_health_report_on_regression(_on_regression)

        _observation_anchor = d.pop("observation_anchor", UNSET)
        observation_anchor: datetime.datetime | Unset
        if isinstance(_observation_anchor, Unset):
            observation_anchor = UNSET
        else:
            observation_anchor = datetime.datetime.fromisoformat(_observation_anchor)

        minimum_latency_requests = d.pop("minimum_latency_requests", UNSET)

        _profile_signal = d.pop("profile_signal", UNSET)
        profile_signal: CanaryProfileSignal | Unset
        if isinstance(_profile_signal, Unset):
            profile_signal = UNSET
        else:
            profile_signal = CanaryProfileSignal.from_dict(_profile_signal)

        route_health_report = cls(
            app_id=app_id,
            deployment_id=deployment_id,
            candidate_commit_sha=candidate_commit_sha,
            stable_deployment_id=stable_deployment_id,
            stable_commit_sha=stable_commit_sha,
            canary_step=canary_step,
            mode=mode,
            revision=revision,
            checked_at=checked_at,
            coverage=coverage,
            status=status,
            reason=reason,
            minimum_requests=minimum_requests,
            routes=routes,
            client_error_status=client_error_status,
            client_error_reason=client_error_reason,
            customers=customers,
            on_regression=on_regression,
            observation_anchor=observation_anchor,
            minimum_latency_requests=minimum_latency_requests,
            profile_signal=profile_signal,
        )

        route_health_report.additional_properties = d
        return route_health_report

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
