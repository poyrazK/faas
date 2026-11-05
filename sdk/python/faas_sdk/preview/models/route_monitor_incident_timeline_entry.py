from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_monitor_incident_timeline_entry_coverage import (
    RouteMonitorIncidentTimelineEntryCoverage,
    check_route_monitor_incident_timeline_entry_coverage,
)
from ..models.route_monitor_incident_timeline_entry_status import (
    RouteMonitorIncidentTimelineEntryStatus,
    check_route_monitor_incident_timeline_entry_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_monitor_customer_impact import RouteMonitorCustomerImpact
    from ..models.route_monitor_incident_timeline_route import RouteMonitorIncidentTimelineRoute


T = TypeVar("T", bound="RouteMonitorIncidentTimelineEntry")


@_attrs_define
class RouteMonitorIncidentTimelineEntry:
    """One confirmed production monitor evaluation. It contains no customer identifiers or request details."""

    checked_at: datetime.datetime
    coverage: RouteMonitorIncidentTimelineEntryCoverage
    status: RouteMonitorIncidentTimelineEntryStatus
    reason: str
    routes: list[RouteMonitorIncidentTimelineRoute]
    customer_impact: RouteMonitorCustomerImpact | Unset = UNSET
    """Aggregate observed request-time identity counts; never includes customer IDs."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        checked_at = self.checked_at.isoformat()

        coverage: str = self.coverage

        status: str = self.status

        reason = self.reason

        routes = []
        for routes_item_data in self.routes:
            routes_item = routes_item_data.to_dict()
            routes.append(routes_item)

        customer_impact: dict[str, Any] | Unset = UNSET
        if not isinstance(self.customer_impact, Unset):
            customer_impact = self.customer_impact.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "checked_at": checked_at,
                "coverage": coverage,
                "status": status,
                "reason": reason,
                "routes": routes,
            }
        )
        if customer_impact is not UNSET:
            field_dict["customer_impact"] = customer_impact

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_monitor_customer_impact import RouteMonitorCustomerImpact
        from ..models.route_monitor_incident_timeline_route import RouteMonitorIncidentTimelineRoute

        d = dict(src_dict)
        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        coverage = check_route_monitor_incident_timeline_entry_coverage(d.pop("coverage"))

        status = check_route_monitor_incident_timeline_entry_status(d.pop("status"))

        reason = d.pop("reason")

        routes = []
        _routes = d.pop("routes")
        for routes_item_data in _routes:
            routes_item = RouteMonitorIncidentTimelineRoute.from_dict(routes_item_data)

            routes.append(routes_item)

        _customer_impact = d.pop("customer_impact", UNSET)
        customer_impact: RouteMonitorCustomerImpact | Unset
        if isinstance(_customer_impact, Unset):
            customer_impact = UNSET
        else:
            customer_impact = RouteMonitorCustomerImpact.from_dict(_customer_impact)

        route_monitor_incident_timeline_entry = cls(
            checked_at=checked_at,
            coverage=coverage,
            status=status,
            reason=reason,
            routes=routes,
            customer_impact=customer_impact,
        )

        route_monitor_incident_timeline_entry.additional_properties = d
        return route_monitor_incident_timeline_entry

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
