from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_health_finding_error_status import (
    RouteHealthFindingErrorStatus,
    check_route_health_finding_error_status,
)
from ..models.route_health_finding_latency_status import (
    RouteHealthFindingLatencyStatus,
    check_route_health_finding_latency_status,
)
from ..models.route_health_finding_status import RouteHealthFindingStatus, check_route_health_finding_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_health_window_evidence import RouteHealthWindowEvidence


T = TypeVar("T", bound="RouteHealthFinding")


@_attrs_define
class RouteHealthFinding:
    """Combined verdict and both closed-window evidence records for one selected critical route."""

    method: str
    path: str
    status: RouteHealthFindingStatus
    reason: str
    windows: list[RouteHealthWindowEvidence]
    check_latency: bool | Unset = UNSET
    """Whether the relative p95 slowdown check is selected."""
    max_p95_ms: int | Unset = UNSET
    """Absolute candidate p95 budget in milliseconds; zero or omitted disables this check."""
    error_status: RouteHealthFindingErrorStatus | Unset = UNSET
    error_reason: str | Unset = UNSET
    latency_status: RouteHealthFindingLatencyStatus | Unset = UNSET
    """Independently confirmed latency verdict across both windows when selected."""
    latency_reason: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        method = self.method

        path = self.path

        status: str = self.status

        reason = self.reason

        windows = []
        for windows_item_data in self.windows:
            windows_item = windows_item_data.to_dict()
            windows.append(windows_item)

        check_latency = self.check_latency

        max_p95_ms = self.max_p95_ms

        error_status: str | Unset = UNSET
        if not isinstance(self.error_status, Unset):
            error_status = self.error_status

        error_reason = self.error_reason

        latency_status: str | Unset = UNSET
        if not isinstance(self.latency_status, Unset):
            latency_status = self.latency_status

        latency_reason = self.latency_reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "method": method,
                "path": path,
                "status": status,
                "reason": reason,
                "windows": windows,
            }
        )
        if check_latency is not UNSET:
            field_dict["check_latency"] = check_latency
        if max_p95_ms is not UNSET:
            field_dict["max_p95_ms"] = max_p95_ms
        if error_status is not UNSET:
            field_dict["error_status"] = error_status
        if error_reason is not UNSET:
            field_dict["error_reason"] = error_reason
        if latency_status is not UNSET:
            field_dict["latency_status"] = latency_status
        if latency_reason is not UNSET:
            field_dict["latency_reason"] = latency_reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_health_window_evidence import RouteHealthWindowEvidence

        d = dict(src_dict)
        method = d.pop("method")

        path = d.pop("path")

        status = check_route_health_finding_status(d.pop("status"))

        reason = d.pop("reason")

        windows = []
        _windows = d.pop("windows")
        for windows_item_data in _windows:
            windows_item = RouteHealthWindowEvidence.from_dict(windows_item_data)

            windows.append(windows_item)

        check_latency = d.pop("check_latency", UNSET)

        max_p95_ms = d.pop("max_p95_ms", UNSET)

        _error_status = d.pop("error_status", UNSET)
        error_status: RouteHealthFindingErrorStatus | Unset
        if isinstance(_error_status, Unset):
            error_status = UNSET
        else:
            error_status = check_route_health_finding_error_status(_error_status)

        error_reason = d.pop("error_reason", UNSET)

        _latency_status = d.pop("latency_status", UNSET)
        latency_status: RouteHealthFindingLatencyStatus | Unset
        if isinstance(_latency_status, Unset):
            latency_status = UNSET
        else:
            latency_status = check_route_health_finding_latency_status(_latency_status)

        latency_reason = d.pop("latency_reason", UNSET)

        route_health_finding = cls(
            method=method,
            path=path,
            status=status,
            reason=reason,
            windows=windows,
            check_latency=check_latency,
            max_p95_ms=max_p95_ms,
            error_status=error_status,
            error_reason=error_reason,
            latency_status=latency_status,
            latency_reason=latency_reason,
        )

        route_health_finding.additional_properties = d
        return route_health_finding

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
