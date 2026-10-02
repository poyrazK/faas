from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_health_window_evidence_error_status import (
    RouteHealthWindowEvidenceErrorStatus,
    check_route_health_window_evidence_error_status,
)
from ..models.route_health_window_evidence_latency_status import (
    RouteHealthWindowEvidenceLatencyStatus,
    check_route_health_window_evidence_latency_status,
)
from ..models.route_health_window_evidence_status import (
    RouteHealthWindowEvidenceStatus,
    check_route_health_window_evidence_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_health_counts import RouteHealthCounts


T = TypeVar("T", bound="RouteHealthWindowEvidence")


@_attrs_define
class RouteHealthWindowEvidence:
    """Candidate and stable observations within one closed window, with independent error and latency verdicts."""

    start: datetime.datetime
    end: datetime.datetime
    candidate: RouteHealthCounts
    """Represented request and server-error counts, error rate, and optional weighted p95 for one deployment in one
    window."""
    stable: RouteHealthCounts
    """Represented request and server-error counts, error rate, and optional weighted p95 for one deployment in one
    window."""
    status: RouteHealthWindowEvidenceStatus
    reason: str
    error_status: RouteHealthWindowEvidenceErrorStatus | Unset = UNSET
    error_reason: str | Unset = UNSET
    latency_status: RouteHealthWindowEvidenceLatencyStatus | Unset = UNSET
    """Present when either latency check is selected."""
    latency_reason: str | Unset = UNSET
    latency_delta_ms: float | Unset = UNSET
    """Candidate p95 minus stable p95 in milliseconds when latency evidence is sufficient."""
    latency_factor: float | Unset = UNSET
    """Candidate p95 divided by stable p95. Omitted when stable p95 is zero or evidence is insufficient."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        start = self.start.isoformat()

        end = self.end.isoformat()

        candidate = self.candidate.to_dict()

        stable = self.stable.to_dict()

        status: str = self.status

        reason = self.reason

        error_status: str | Unset = UNSET
        if not isinstance(self.error_status, Unset):
            error_status = self.error_status

        error_reason = self.error_reason

        latency_status: str | Unset = UNSET
        if not isinstance(self.latency_status, Unset):
            latency_status = self.latency_status

        latency_reason = self.latency_reason

        latency_delta_ms = self.latency_delta_ms

        latency_factor = self.latency_factor

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "start": start,
                "end": end,
                "candidate": candidate,
                "stable": stable,
                "status": status,
                "reason": reason,
            }
        )
        if error_status is not UNSET:
            field_dict["error_status"] = error_status
        if error_reason is not UNSET:
            field_dict["error_reason"] = error_reason
        if latency_status is not UNSET:
            field_dict["latency_status"] = latency_status
        if latency_reason is not UNSET:
            field_dict["latency_reason"] = latency_reason
        if latency_delta_ms is not UNSET:
            field_dict["latency_delta_ms"] = latency_delta_ms
        if latency_factor is not UNSET:
            field_dict["latency_factor"] = latency_factor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_health_counts import RouteHealthCounts

        d = dict(src_dict)
        start = datetime.datetime.fromisoformat(d.pop("start"))

        end = datetime.datetime.fromisoformat(d.pop("end"))

        candidate = RouteHealthCounts.from_dict(d.pop("candidate"))

        stable = RouteHealthCounts.from_dict(d.pop("stable"))

        status = check_route_health_window_evidence_status(d.pop("status"))

        reason = d.pop("reason")

        _error_status = d.pop("error_status", UNSET)
        error_status: RouteHealthWindowEvidenceErrorStatus | Unset
        if isinstance(_error_status, Unset):
            error_status = UNSET
        else:
            error_status = check_route_health_window_evidence_error_status(_error_status)

        error_reason = d.pop("error_reason", UNSET)

        _latency_status = d.pop("latency_status", UNSET)
        latency_status: RouteHealthWindowEvidenceLatencyStatus | Unset
        if isinstance(_latency_status, Unset):
            latency_status = UNSET
        else:
            latency_status = check_route_health_window_evidence_latency_status(_latency_status)

        latency_reason = d.pop("latency_reason", UNSET)

        latency_delta_ms = d.pop("latency_delta_ms", UNSET)

        latency_factor = d.pop("latency_factor", UNSET)

        route_health_window_evidence = cls(
            start=start,
            end=end,
            candidate=candidate,
            stable=stable,
            status=status,
            reason=reason,
            error_status=error_status,
            error_reason=error_reason,
            latency_status=latency_status,
            latency_reason=latency_reason,
            latency_delta_ms=latency_delta_ms,
            latency_factor=latency_factor,
        )

        route_health_window_evidence.additional_properties = d
        return route_health_window_evidence

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
