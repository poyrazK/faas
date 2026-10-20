from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_monitor_report_coverage import RouteMonitorReportCoverage, check_route_monitor_report_coverage
from ..models.route_monitor_report_customer_group_by import (
    RouteMonitorReportCustomerGroupBy,
    check_route_monitor_report_customer_group_by,
)
from ..models.route_monitor_report_status import RouteMonitorReportStatus, check_route_monitor_report_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_monitor_customer_report import RouteMonitorCustomerReport
    from ..models.route_monitor_finding import RouteMonitorFinding


T = TypeVar("T", bound="RouteMonitorReport")


@_attrs_define
class RouteMonitorReport:
    """Read-only absolute-budget evidence for the sole fully serving default-scope deployment. Coverage is limited to
    stored telemetry.

    """

    version: int
    app_id: UUID
    enabled: bool
    revision: int
    checked_at: datetime.datetime
    coverage: RouteMonitorReportCoverage
    status: RouteMonitorReportStatus
    reason: str
    minimum_requests: int
    minimum_latency_requests: int
    routes: list[RouteMonitorFinding]
    customer_group_by: RouteMonitorReportCustomerGroupBy | Unset = UNSET
    deployment_id: UUID | Unset = UNSET
    commit_sha: str | Unset = UNSET
    observation_anchor: datetime.datetime | Unset = UNSET
    customers: RouteMonitorCustomerReport | Unset = UNSET
    """Request-time tenant or API-consumer budget summary. Identity details are redacted unless explicitly
    requested."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        app_id = str(self.app_id)

        enabled = self.enabled

        revision = self.revision

        checked_at = self.checked_at.isoformat()

        coverage: str = self.coverage

        status: str = self.status

        reason = self.reason

        minimum_requests = self.minimum_requests

        minimum_latency_requests = self.minimum_latency_requests

        routes = []
        for routes_item_data in self.routes:
            routes_item = routes_item_data.to_dict()
            routes.append(routes_item)

        customer_group_by: str | Unset = UNSET
        if not isinstance(self.customer_group_by, Unset):
            customer_group_by = self.customer_group_by

        deployment_id: str | Unset = UNSET
        if not isinstance(self.deployment_id, Unset):
            deployment_id = str(self.deployment_id)

        commit_sha = self.commit_sha

        observation_anchor: str | Unset = UNSET
        if not isinstance(self.observation_anchor, Unset):
            observation_anchor = self.observation_anchor.isoformat()

        customers: dict[str, Any] | Unset = UNSET
        if not isinstance(self.customers, Unset):
            customers = self.customers.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "app_id": app_id,
                "enabled": enabled,
                "revision": revision,
                "checked_at": checked_at,
                "coverage": coverage,
                "status": status,
                "reason": reason,
                "minimum_requests": minimum_requests,
                "minimum_latency_requests": minimum_latency_requests,
                "routes": routes,
            }
        )
        if customer_group_by is not UNSET:
            field_dict["customer_group_by"] = customer_group_by
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if commit_sha is not UNSET:
            field_dict["commit_sha"] = commit_sha
        if observation_anchor is not UNSET:
            field_dict["observation_anchor"] = observation_anchor
        if customers is not UNSET:
            field_dict["customers"] = customers

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_monitor_customer_report import RouteMonitorCustomerReport
        from ..models.route_monitor_finding import RouteMonitorFinding

        d = dict(src_dict)
        version = d.pop("version")

        app_id = UUID(d.pop("app_id"))

        enabled = d.pop("enabled")

        revision = d.pop("revision")

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        coverage = check_route_monitor_report_coverage(d.pop("coverage"))

        status = check_route_monitor_report_status(d.pop("status"))

        reason = d.pop("reason")

        minimum_requests = d.pop("minimum_requests")

        minimum_latency_requests = d.pop("minimum_latency_requests")

        routes = []
        _routes = d.pop("routes")
        for routes_item_data in _routes:
            routes_item = RouteMonitorFinding.from_dict(routes_item_data)

            routes.append(routes_item)

        _customer_group_by = d.pop("customer_group_by", UNSET)
        customer_group_by: RouteMonitorReportCustomerGroupBy | Unset
        if isinstance(_customer_group_by, Unset):
            customer_group_by = UNSET
        else:
            customer_group_by = check_route_monitor_report_customer_group_by(_customer_group_by)

        _deployment_id = d.pop("deployment_id", UNSET)
        deployment_id: UUID | Unset
        if isinstance(_deployment_id, Unset):
            deployment_id = UNSET
        else:
            deployment_id = UUID(_deployment_id)

        commit_sha = d.pop("commit_sha", UNSET)

        _observation_anchor = d.pop("observation_anchor", UNSET)
        observation_anchor: datetime.datetime | Unset
        if isinstance(_observation_anchor, Unset):
            observation_anchor = UNSET
        else:
            observation_anchor = datetime.datetime.fromisoformat(_observation_anchor)

        _customers = d.pop("customers", UNSET)
        customers: RouteMonitorCustomerReport | Unset
        if isinstance(_customers, Unset):
            customers = UNSET
        else:
            customers = RouteMonitorCustomerReport.from_dict(_customers)

        route_monitor_report = cls(
            version=version,
            app_id=app_id,
            enabled=enabled,
            revision=revision,
            checked_at=checked_at,
            coverage=coverage,
            status=status,
            reason=reason,
            minimum_requests=minimum_requests,
            minimum_latency_requests=minimum_latency_requests,
            routes=routes,
            customer_group_by=customer_group_by,
            deployment_id=deployment_id,
            commit_sha=commit_sha,
            observation_anchor=observation_anchor,
            customers=customers,
        )

        route_monitor_report.additional_properties = d
        return route_monitor_report

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
