from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.debug_coverage_signal import DebugCoverageSignal


T = TypeVar("T", bound="DebugCoverageResponse")


@_attrs_define
class DebugCoverageResponse:
    """Customer-safe debugger coverage summary. This is observed coverage
    of retained telemetry, not a claim that every gateway request was
    persisted.

    """

    app_id: UUID
    since: str
    window_start: datetime.datetime
    window_end: datetime.datetime
    plan_retention_days: int
    telemetry_rows: int
    represented_requests: int
    error_requests: int
    trace_linked: DebugCoverageSignal
    """Observed coverage for one debugger signal. Requests are weighted by collapsed-row count."""
    span_evidence: DebugCoverageSignal
    """Observed coverage for one debugger signal. Requests are weighted by collapsed-row count."""
    wake_evidence: DebugCoverageSignal
    """Observed coverage for one debugger signal. Requests are weighted by collapsed-row count."""
    guest_evidence: DebugCoverageSignal
    """Observed coverage for one debugger signal. Requests are weighted by collapsed-row count."""
    oldest_telemetry_at: datetime.datetime | Unset = UNSET
    latest_telemetry_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        since = self.since

        window_start = self.window_start.isoformat()

        window_end = self.window_end.isoformat()

        plan_retention_days = self.plan_retention_days

        telemetry_rows = self.telemetry_rows

        represented_requests = self.represented_requests

        error_requests = self.error_requests

        trace_linked = self.trace_linked.to_dict()

        span_evidence = self.span_evidence.to_dict()

        wake_evidence = self.wake_evidence.to_dict()

        guest_evidence = self.guest_evidence.to_dict()

        oldest_telemetry_at: str | Unset = UNSET
        if not isinstance(self.oldest_telemetry_at, Unset):
            oldest_telemetry_at = self.oldest_telemetry_at.isoformat()

        latest_telemetry_at: str | Unset = UNSET
        if not isinstance(self.latest_telemetry_at, Unset):
            latest_telemetry_at = self.latest_telemetry_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "since": since,
                "window_start": window_start,
                "window_end": window_end,
                "plan_retention_days": plan_retention_days,
                "telemetry_rows": telemetry_rows,
                "represented_requests": represented_requests,
                "error_requests": error_requests,
                "trace_linked": trace_linked,
                "span_evidence": span_evidence,
                "wake_evidence": wake_evidence,
                "guest_evidence": guest_evidence,
            }
        )
        if oldest_telemetry_at is not UNSET:
            field_dict["oldest_telemetry_at"] = oldest_telemetry_at
        if latest_telemetry_at is not UNSET:
            field_dict["latest_telemetry_at"] = latest_telemetry_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.debug_coverage_signal import DebugCoverageSignal

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        since = d.pop("since")

        window_start = datetime.datetime.fromisoformat(d.pop("window_start"))

        window_end = datetime.datetime.fromisoformat(d.pop("window_end"))

        plan_retention_days = d.pop("plan_retention_days")

        telemetry_rows = d.pop("telemetry_rows")

        represented_requests = d.pop("represented_requests")

        error_requests = d.pop("error_requests")

        trace_linked = DebugCoverageSignal.from_dict(d.pop("trace_linked"))

        span_evidence = DebugCoverageSignal.from_dict(d.pop("span_evidence"))

        wake_evidence = DebugCoverageSignal.from_dict(d.pop("wake_evidence"))

        guest_evidence = DebugCoverageSignal.from_dict(d.pop("guest_evidence"))

        _oldest_telemetry_at = d.pop("oldest_telemetry_at", UNSET)
        oldest_telemetry_at: datetime.datetime | Unset
        if isinstance(_oldest_telemetry_at, Unset):
            oldest_telemetry_at = UNSET
        else:
            oldest_telemetry_at = datetime.datetime.fromisoformat(_oldest_telemetry_at)

        _latest_telemetry_at = d.pop("latest_telemetry_at", UNSET)
        latest_telemetry_at: datetime.datetime | Unset
        if isinstance(_latest_telemetry_at, Unset):
            latest_telemetry_at = UNSET
        else:
            latest_telemetry_at = datetime.datetime.fromisoformat(_latest_telemetry_at)

        debug_coverage_response = cls(
            app_id=app_id,
            since=since,
            window_start=window_start,
            window_end=window_end,
            plan_retention_days=plan_retention_days,
            telemetry_rows=telemetry_rows,
            represented_requests=represented_requests,
            error_requests=error_requests,
            trace_linked=trace_linked,
            span_evidence=span_evidence,
            wake_evidence=wake_evidence,
            guest_evidence=guest_evidence,
            oldest_telemetry_at=oldest_telemetry_at,
            latest_telemetry_at=latest_telemetry_at,
        )

        debug_coverage_response.additional_properties = d
        return debug_coverage_response

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
