from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_monitor_incident_timeline_route_error_status import (
    RouteMonitorIncidentTimelineRouteErrorStatus,
    check_route_monitor_incident_timeline_route_error_status,
)
from ..models.route_monitor_incident_timeline_route_latency_status import (
    RouteMonitorIncidentTimelineRouteLatencyStatus,
    check_route_monitor_incident_timeline_route_latency_status,
)
from ..models.route_monitor_incident_timeline_route_status import (
    RouteMonitorIncidentTimelineRouteStatus,
    check_route_monitor_incident_timeline_route_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_monitor_customer_impact import RouteMonitorCustomerImpact


T = TypeVar("T", bound="RouteMonitorIncidentTimelineRoute")


@_attrs_define
class RouteMonitorIncidentTimelineRoute:
    """Status for one route in the incident's fixed opening selector order."""

    route_index: int
    status: RouteMonitorIncidentTimelineRouteStatus
    error_status: RouteMonitorIncidentTimelineRouteErrorStatus
    latency_status: RouteMonitorIncidentTimelineRouteLatencyStatus
    customer_impact: RouteMonitorCustomerImpact | Unset = UNSET
    """Aggregate observed request-time identity counts; never includes customer IDs."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        route_index = self.route_index

        status: str = self.status

        error_status: str = self.error_status

        latency_status: str = self.latency_status

        customer_impact: dict[str, Any] | Unset = UNSET
        if not isinstance(self.customer_impact, Unset):
            customer_impact = self.customer_impact.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "route_index": route_index,
                "status": status,
                "error_status": error_status,
                "latency_status": latency_status,
            }
        )
        if customer_impact is not UNSET:
            field_dict["customer_impact"] = customer_impact

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_monitor_customer_impact import RouteMonitorCustomerImpact

        d = dict(src_dict)
        route_index = d.pop("route_index")

        status = check_route_monitor_incident_timeline_route_status(d.pop("status"))

        error_status = check_route_monitor_incident_timeline_route_error_status(d.pop("error_status"))

        latency_status = check_route_monitor_incident_timeline_route_latency_status(d.pop("latency_status"))

        _customer_impact = d.pop("customer_impact", UNSET)
        customer_impact: RouteMonitorCustomerImpact | Unset
        if isinstance(_customer_impact, Unset):
            customer_impact = UNSET
        else:
            customer_impact = RouteMonitorCustomerImpact.from_dict(_customer_impact)

        route_monitor_incident_timeline_route = cls(
            route_index=route_index,
            status=status,
            error_status=error_status,
            latency_status=latency_status,
            customer_impact=customer_impact,
        )

        route_monitor_incident_timeline_route.additional_properties = d
        return route_monitor_incident_timeline_route

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
