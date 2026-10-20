from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_monitor_incident_escalation_signal_signal import (
    RouteMonitorIncidentEscalationSignalSignal,
    check_route_monitor_incident_escalation_signal_signal,
)

if TYPE_CHECKING:
    from ..models.route_monitor_finding import RouteMonitorFinding


T = TypeVar("T", bound="RouteMonitorIncidentEscalationSignal")


@_attrs_define
class RouteMonitorIncidentEscalationSignal:
    """A route/signal that changed from non-violated to violated. Route indexes refer to opening_report.routes."""

    route_index: int
    signal: RouteMonitorIncidentEscalationSignalSignal
    finding: RouteMonitorFinding
    """One production route and two windows with independently confirmed error and latency verdicts."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        route_index = self.route_index

        signal: str = self.signal

        finding = self.finding.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "route_index": route_index,
                "signal": signal,
                "finding": finding,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_monitor_finding import RouteMonitorFinding

        d = dict(src_dict)
        route_index = d.pop("route_index")

        signal = check_route_monitor_incident_escalation_signal_signal(d.pop("signal"))

        finding = RouteMonitorFinding.from_dict(d.pop("finding"))

        route_monitor_incident_escalation_signal = cls(
            route_index=route_index,
            signal=signal,
            finding=finding,
        )

        route_monitor_incident_escalation_signal.additional_properties = d
        return route_monitor_incident_escalation_signal

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
