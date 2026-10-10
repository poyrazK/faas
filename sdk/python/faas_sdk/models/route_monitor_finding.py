from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_monitor_finding_error_status import (
    RouteMonitorFindingErrorStatus,
    check_route_monitor_finding_error_status,
)
from ..models.route_monitor_finding_evidence_window import (
    RouteMonitorFindingEvidenceWindow,
    check_route_monitor_finding_evidence_window,
)
from ..models.route_monitor_finding_latency_status import (
    RouteMonitorFindingLatencyStatus,
    check_route_monitor_finding_latency_status,
)
from ..models.route_monitor_finding_status import RouteMonitorFindingStatus, check_route_monitor_finding_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_monitor_route import RouteMonitorRoute
    from ..models.route_monitor_window import RouteMonitorWindow


T = TypeVar("T", bound="RouteMonitorFinding")


@_attrs_define
class RouteMonitorFinding:
    """One production route and two windows with independently confirmed error and latency verdicts."""

    route: RouteMonitorRoute
    """Exact normalized production telemetry label with absolute budgets. Omitted max_5xx_rate_bps disables errors;
    zero is a selected zero-error budget. Zero or omitted max_p95_ms disables latency. At least one budget is
    required."""
    status: RouteMonitorFindingStatus
    reason: str
    error_status: RouteMonitorFindingErrorStatus
    latency_status: RouteMonitorFindingLatencyStatus
    windows: list[RouteMonitorWindow]
    evidence_window: RouteMonitorFindingEvidenceWindow | Unset = UNSET
    """Present when the verdict comes from pooled_windows because the one-minute windows lacked requests (ADR-953).
    Budgets are unchanged."""
    pooled_windows: list[RouteMonitorWindow] | Unset = UNSET
    """Two consecutive halves of up to the newest 30 minutes since the observation anchor, read only for routes
    whose one-minute windows were sparse."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        route = self.route.to_dict()

        status: str = self.status

        reason = self.reason

        error_status: str = self.error_status

        latency_status: str = self.latency_status

        windows = []
        for windows_item_data in self.windows:
            windows_item = windows_item_data.to_dict()
            windows.append(windows_item)

        evidence_window: str | Unset = UNSET
        if not isinstance(self.evidence_window, Unset):
            evidence_window = self.evidence_window

        pooled_windows: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.pooled_windows, Unset):
            pooled_windows = []
            for pooled_windows_item_data in self.pooled_windows:
                pooled_windows_item = pooled_windows_item_data.to_dict()
                pooled_windows.append(pooled_windows_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "route": route,
                "status": status,
                "reason": reason,
                "error_status": error_status,
                "latency_status": latency_status,
                "windows": windows,
            }
        )
        if evidence_window is not UNSET:
            field_dict["evidence_window"] = evidence_window
        if pooled_windows is not UNSET:
            field_dict["pooled_windows"] = pooled_windows

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_monitor_route import RouteMonitorRoute
        from ..models.route_monitor_window import RouteMonitorWindow

        d = dict(src_dict)
        route = RouteMonitorRoute.from_dict(d.pop("route"))

        status = check_route_monitor_finding_status(d.pop("status"))

        reason = d.pop("reason")

        error_status = check_route_monitor_finding_error_status(d.pop("error_status"))

        latency_status = check_route_monitor_finding_latency_status(d.pop("latency_status"))

        windows = []
        _windows = d.pop("windows")
        for windows_item_data in _windows:
            windows_item = RouteMonitorWindow.from_dict(windows_item_data)

            windows.append(windows_item)

        _evidence_window = d.pop("evidence_window", UNSET)
        evidence_window: RouteMonitorFindingEvidenceWindow | Unset
        if isinstance(_evidence_window, Unset):
            evidence_window = UNSET
        else:
            evidence_window = check_route_monitor_finding_evidence_window(_evidence_window)

        _pooled_windows = d.pop("pooled_windows", UNSET)
        pooled_windows: list[RouteMonitorWindow] | Unset = UNSET
        if _pooled_windows is not UNSET:
            pooled_windows = []
            for pooled_windows_item_data in _pooled_windows:
                pooled_windows_item = RouteMonitorWindow.from_dict(pooled_windows_item_data)

                pooled_windows.append(pooled_windows_item)

        route_monitor_finding = cls(
            route=route,
            status=status,
            reason=reason,
            error_status=error_status,
            latency_status=latency_status,
            windows=windows,
            evidence_window=evidence_window,
            pooled_windows=pooled_windows,
        )

        route_monitor_finding.additional_properties = d
        return route_monitor_finding

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
