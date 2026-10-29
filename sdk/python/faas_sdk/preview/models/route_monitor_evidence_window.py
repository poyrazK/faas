from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_health_investigation_side import RouteHealthInvestigationSide
    from ..models.route_health_latency_diagnostics import RouteHealthLatencyDiagnostics


T = TypeVar("T", bound="RouteMonitorEvidenceWindow")


@_attrs_define
class RouteMonitorEvidenceWindow:
    """Saved bounded request examples and optional one-sided dependency diagnostics for an opening window."""

    start: datetime.datetime
    end: datetime.datetime
    requests: RouteHealthInvestigationSide
    """Matching response weights and row inventory for one deployment/window, counted before the example cap. Error
    examples prioritize trace-linked rows, then newest timestamp and descending telemetry UUID. Latency examples
    prioritize the slowest latency bucket, then those same ties."""
    diagnostics: RouteHealthLatencyDiagnostics | Unset = UNSET
    """Newest 32 retained rows per deployment/window, including rows without spans. Normalized type/kind groups
    exclude names, SQL and attributes. Sample percentiles are not additive and do not establish cause or complete
    capture."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        start = self.start.isoformat()

        end = self.end.isoformat()

        requests = self.requests.to_dict()

        diagnostics: dict[str, Any] | Unset = UNSET
        if not isinstance(self.diagnostics, Unset):
            diagnostics = self.diagnostics.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "start": start,
                "end": end,
                "requests": requests,
            }
        )
        if diagnostics is not UNSET:
            field_dict["diagnostics"] = diagnostics

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_health_investigation_side import RouteHealthInvestigationSide
        from ..models.route_health_latency_diagnostics import RouteHealthLatencyDiagnostics

        d = dict(src_dict)
        start = datetime.datetime.fromisoformat(d.pop("start"))

        end = datetime.datetime.fromisoformat(d.pop("end"))

        requests = RouteHealthInvestigationSide.from_dict(d.pop("requests"))

        _diagnostics = d.pop("diagnostics", UNSET)
        diagnostics: RouteHealthLatencyDiagnostics | Unset
        if isinstance(_diagnostics, Unset):
            diagnostics = UNSET
        else:
            diagnostics = RouteHealthLatencyDiagnostics.from_dict(_diagnostics)

        route_monitor_evidence_window = cls(
            start=start,
            end=end,
            requests=requests,
            diagnostics=diagnostics,
        )

        route_monitor_evidence_window.additional_properties = d
        return route_monitor_evidence_window

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
