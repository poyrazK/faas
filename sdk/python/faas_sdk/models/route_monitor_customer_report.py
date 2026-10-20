from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_monitor_customer_report_coverage import (
    RouteMonitorCustomerReportCoverage,
    check_route_monitor_customer_report_coverage,
)
from ..models.route_monitor_customer_report_group_by import (
    RouteMonitorCustomerReportGroupBy,
    check_route_monitor_customer_report_group_by,
)
from ..models.route_monitor_customer_report_status import (
    RouteMonitorCustomerReportStatus,
    check_route_monitor_customer_report_status,
)

if TYPE_CHECKING:
    from ..models.route_monitor_customer_route import RouteMonitorCustomerRoute


T = TypeVar("T", bound="RouteMonitorCustomerReport")


@_attrs_define
class RouteMonitorCustomerReport:
    """Request-time tenant or API-consumer budget summary. Identity details are redacted unless explicitly requested."""

    group_by: RouteMonitorCustomerReportGroupBy
    details_included: bool
    coverage: RouteMonitorCustomerReportCoverage
    status: RouteMonitorCustomerReportStatus
    reason: str
    customers_limit: int
    observed_customers: int
    violated_customers: int
    unknown_customers: int
    recovery_remaining_customers: int
    """Distinct known violating identities not yet observed healthy again."""
    recovery_inventory_incomplete: bool
    routes: list[RouteMonitorCustomerRoute]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        group_by: str = self.group_by

        details_included = self.details_included

        coverage: str = self.coverage

        status: str = self.status

        reason = self.reason

        customers_limit = self.customers_limit

        observed_customers = self.observed_customers

        violated_customers = self.violated_customers

        unknown_customers = self.unknown_customers

        recovery_remaining_customers = self.recovery_remaining_customers

        recovery_inventory_incomplete = self.recovery_inventory_incomplete

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
                "observed_customers": observed_customers,
                "violated_customers": violated_customers,
                "unknown_customers": unknown_customers,
                "recovery_remaining_customers": recovery_remaining_customers,
                "recovery_inventory_incomplete": recovery_inventory_incomplete,
                "routes": routes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_monitor_customer_route import RouteMonitorCustomerRoute

        d = dict(src_dict)
        group_by = check_route_monitor_customer_report_group_by(d.pop("group_by"))

        details_included = d.pop("details_included")

        coverage = check_route_monitor_customer_report_coverage(d.pop("coverage"))

        status = check_route_monitor_customer_report_status(d.pop("status"))

        reason = d.pop("reason")

        customers_limit = d.pop("customers_limit")

        observed_customers = d.pop("observed_customers")

        violated_customers = d.pop("violated_customers")

        unknown_customers = d.pop("unknown_customers")

        recovery_remaining_customers = d.pop("recovery_remaining_customers")

        recovery_inventory_incomplete = d.pop("recovery_inventory_incomplete")

        routes = []
        _routes = d.pop("routes")
        for routes_item_data in _routes:
            routes_item = RouteMonitorCustomerRoute.from_dict(routes_item_data)

            routes.append(routes_item)

        route_monitor_customer_report = cls(
            group_by=group_by,
            details_included=details_included,
            coverage=coverage,
            status=status,
            reason=reason,
            customers_limit=customers_limit,
            observed_customers=observed_customers,
            violated_customers=violated_customers,
            unknown_customers=unknown_customers,
            recovery_remaining_customers=recovery_remaining_customers,
            recovery_inventory_incomplete=recovery_inventory_incomplete,
            routes=routes,
        )

        route_monitor_customer_report.additional_properties = d
        return route_monitor_customer_report

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
