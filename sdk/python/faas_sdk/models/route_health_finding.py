from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_health_finding_error_status import (
    RouteHealthFindingErrorStatus,
    check_route_health_finding_error_status,
)
from ..models.route_health_finding_evidence_window import (
    RouteHealthFindingEvidenceWindow,
    check_route_health_finding_evidence_window,
)
from ..models.route_health_finding_latency_status import (
    RouteHealthFindingLatencyStatus,
    check_route_health_finding_latency_status,
)
from ..models.route_health_finding_status import RouteHealthFindingStatus, check_route_health_finding_status
from ..models.route_health_finding_watch_statuses_item import (
    RouteHealthFindingWatchStatusesItem,
    check_route_health_finding_watch_statuses_item,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_health_client_error_report import RouteHealthClientErrorReport
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
    client_errors: RouteHealthClientErrorReport | Unset = UNSET
    """Live advisory watched-code evidence, independent of the 5xx/latency verdict. Each code uses at least 20
    represented requests on both deployments per window, two matching regression windows, at least two candidate
    responses, a rate of at least 5 percent, three times stable, and at least five percentage points above stable.
    Stable expected rejection rates remain healthy. Sparse, one-sided, missing or pre-anchor evidence is unknown.
    Coverage inherits observed_only from the containing report; these observations neither prove a defect nor
    certify an SLO. Excluded from saved decisions, recovery and webhook payloads."""
    watch_statuses: list[RouteHealthFindingWatchStatusesItem] | Unset = UNSET
    """Configured watched response codes for this exact route. Live client_errors contains their independent
    advisory evidence; saved decisions retain selectors without code evidence."""
    check_latency: bool | Unset = UNSET
    """Whether the relative p95 slowdown check is selected."""
    max_p95_ms: int | Unset = UNSET
    """Absolute candidate p95 budget in milliseconds; zero or omitted disables this check."""
    error_status: RouteHealthFindingErrorStatus | Unset = UNSET
    error_reason: str | Unset = UNSET
    latency_status: RouteHealthFindingLatencyStatus | Unset = UNSET
    """Independently confirmed latency verdict across both windows when selected."""
    latency_reason: str | Unset = UNSET
    evidence_window: RouteHealthFindingEvidenceWindow | Unset = UNSET
    """Present when the verdict comes from pooled_windows because the one-minute windows lacked requests (ADR-846).
    Thresholds are unchanged."""
    pooled_windows: list[RouteHealthWindowEvidence] | Unset = UNSET
    """Two consecutive halves of up to the newest 30 minutes of the stage, read only for routes whose one-minute
    windows were sparse."""
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

        client_errors: dict[str, Any] | Unset = UNSET
        if not isinstance(self.client_errors, Unset):
            client_errors = self.client_errors.to_dict()

        watch_statuses: list[int] | Unset = UNSET
        if not isinstance(self.watch_statuses, Unset):
            watch_statuses = []
            for watch_statuses_item_data in self.watch_statuses:
                watch_statuses_item: int = watch_statuses_item_data
                watch_statuses.append(watch_statuses_item)

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
                "method": method,
                "path": path,
                "status": status,
                "reason": reason,
                "windows": windows,
            }
        )
        if client_errors is not UNSET:
            field_dict["client_errors"] = client_errors
        if watch_statuses is not UNSET:
            field_dict["watch_statuses"] = watch_statuses
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
        if evidence_window is not UNSET:
            field_dict["evidence_window"] = evidence_window
        if pooled_windows is not UNSET:
            field_dict["pooled_windows"] = pooled_windows

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_health_client_error_report import RouteHealthClientErrorReport
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

        _client_errors = d.pop("client_errors", UNSET)
        client_errors: RouteHealthClientErrorReport | Unset
        if isinstance(_client_errors, Unset):
            client_errors = UNSET
        else:
            client_errors = RouteHealthClientErrorReport.from_dict(_client_errors)

        _watch_statuses = d.pop("watch_statuses", UNSET)
        watch_statuses: list[RouteHealthFindingWatchStatusesItem] | Unset = UNSET
        if _watch_statuses is not UNSET:
            watch_statuses = []
            for watch_statuses_item_data in _watch_statuses:
                watch_statuses_item = check_route_health_finding_watch_statuses_item(watch_statuses_item_data)

                watch_statuses.append(watch_statuses_item)

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

        _evidence_window = d.pop("evidence_window", UNSET)
        evidence_window: RouteHealthFindingEvidenceWindow | Unset
        if isinstance(_evidence_window, Unset):
            evidence_window = UNSET
        else:
            evidence_window = check_route_health_finding_evidence_window(_evidence_window)

        _pooled_windows = d.pop("pooled_windows", UNSET)
        pooled_windows: list[RouteHealthWindowEvidence] | Unset = UNSET
        if _pooled_windows is not UNSET:
            pooled_windows = []
            for pooled_windows_item_data in _pooled_windows:
                pooled_windows_item = RouteHealthWindowEvidence.from_dict(pooled_windows_item_data)

                pooled_windows.append(pooled_windows_item)

        route_health_finding = cls(
            method=method,
            path=path,
            status=status,
            reason=reason,
            windows=windows,
            client_errors=client_errors,
            watch_statuses=watch_statuses,
            check_latency=check_latency,
            max_p95_ms=max_p95_ms,
            error_status=error_status,
            error_reason=error_reason,
            latency_status=latency_status,
            latency_reason=latency_reason,
            evidence_window=evidence_window,
            pooled_windows=pooled_windows,
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
