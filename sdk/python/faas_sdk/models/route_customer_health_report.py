from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_customer_health_report_coverage import (
    RouteCustomerHealthReportCoverage,
    check_route_customer_health_report_coverage,
)
from ..models.route_customer_health_report_group_by import (
    RouteCustomerHealthReportGroupBy,
    check_route_customer_health_report_group_by,
)
from ..models.route_customer_health_report_status import (
    RouteCustomerHealthReportStatus,
    check_route_customer_health_report_status,
)

if TYPE_CHECKING:
    from ..models.route_customer_health_route import RouteCustomerHealthRoute


T = TypeVar("T", bound="RouteCustomerHealthReport")


@_attrs_define
class RouteCustomerHealthReport:
    """Advisory live comparisons in the same repeatable-read snapshot as aggregate health. Reuses sample minima and
    consecutive-window error and selected latency checks per identity, with watched client responses included in the
    advisory customer summary. A confirmed observed regression takes precedence; empty, sparse, capped, unattributed,
    unresolved or unavailable evidence prevents a healthy summary. Healthy means the retained observed cohort
    comparisons passed, not proof of complete capture or a statistical SLO. Request-time tenant IDs never follow current
    consumer links. Revoked consumers remain eligible historical observations. No identities enter decisions, history,
    audits or webhooks.

    """

    group_by: RouteCustomerHealthReportGroupBy
    details_included: bool
    coverage: RouteCustomerHealthReportCoverage
    status: RouteCustomerHealthReportStatus
    reason: str
    customers_limit: int
    routes: list[RouteCustomerHealthRoute]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        group_by: str = self.group_by

        details_included = self.details_included

        coverage: str = self.coverage

        status: str = self.status

        reason = self.reason

        customers_limit = self.customers_limit

        routes = []
        for routes_item_data in self.routes:
            routes_item = routes_item_data.to_dict()
            routes.append(routes_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "group_by": group_by,
                "details_included": details_included,
                "coverage": coverage,
                "status": status,
                "reason": reason,
                "customers_limit": customers_limit,
                "routes": routes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_customer_health_route import RouteCustomerHealthRoute

        d = dict(src_dict)
        group_by = check_route_customer_health_report_group_by(d.pop("group_by"))

        details_included = d.pop("details_included")

        coverage = check_route_customer_health_report_coverage(d.pop("coverage"))

        status = check_route_customer_health_report_status(d.pop("status"))

        reason = d.pop("reason")

        customers_limit = d.pop("customers_limit")

        routes = []
        _routes = d.pop("routes")
        for routes_item_data in _routes:
            routes_item = RouteCustomerHealthRoute.from_dict(routes_item_data)

            routes.append(routes_item)

        route_customer_health_report = cls(
            group_by=group_by,
            details_included=details_included,
            coverage=coverage,
            status=status,
            reason=reason,
            customers_limit=customers_limit,
            routes=routes,
        )

        route_customer_health_report.additional_properties = d
        return route_customer_health_report

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
