from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_health_report_coverage import RouteHealthReportCoverage, check_route_health_report_coverage
from ..models.route_health_report_mode import RouteHealthReportMode, check_route_health_report_mode
from ..models.route_health_report_on_regression import (
    RouteHealthReportOnRegression,
    check_route_health_report_on_regression,
)
from ..models.route_health_report_status import RouteHealthReportStatus, check_route_health_report_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_health_finding import RouteHealthFinding


T = TypeVar("T", bound="RouteHealthReport")


@_attrs_define
class RouteHealthReport:
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
    on_regression: RouteHealthReportOnRegression | Unset = UNSET
    """Defaults to hold, including when omitted in a replacement update. Abort opts into automatic recovery for
    confirmed route 5xx regressions during an enforced canary; report mode is observational."""
    observation_anchor: datetime.datetime | Unset = UNSET
    """Latest stage or configuration timestamp that both windows must follow."""
    minimum_latency_requests: int | Unset = UNSET
    """Present when any route has a latency check selected. Applies to each deployment per route per window."""
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

        on_regression: str | Unset = UNSET
        if not isinstance(self.on_regression, Unset):
            on_regression = self.on_regression

        observation_anchor: str | Unset = UNSET
        if not isinstance(self.observation_anchor, Unset):
            observation_anchor = self.observation_anchor.isoformat()

        minimum_latency_requests = self.minimum_latency_requests

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
        if on_regression is not UNSET:
            field_dict["on_regression"] = on_regression
        if observation_anchor is not UNSET:
            field_dict["observation_anchor"] = observation_anchor
        if minimum_latency_requests is not UNSET:
            field_dict["minimum_latency_requests"] = minimum_latency_requests

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
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
            on_regression=on_regression,
            observation_anchor=observation_anchor,
            minimum_latency_requests=minimum_latency_requests,
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
