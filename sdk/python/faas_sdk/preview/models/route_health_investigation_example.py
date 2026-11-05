from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RouteHealthInvestigationExample")


@_attrs_define
class RouteHealthInvestigationExample:
    """Metadata for one retained matching telemetry row. Its weight may exceed one request; a trace ID is a link rather
    than a promise of retained spans.

    """

    telemetry_id: UUID
    received_at: datetime.datetime
    status: int
    latency_ms: int
    represented_requests: int
    evidence_path: str
    """Authenticated app debugger evidence path for this telemetry row. Retention and authorization are checked
    again when followed."""
    trace_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        telemetry_id = str(self.telemetry_id)

        received_at = self.received_at.isoformat()

        status = self.status

        latency_ms = self.latency_ms

        represented_requests = self.represented_requests

        evidence_path = self.evidence_path

        trace_id = self.trace_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "telemetry_id": telemetry_id,
                "received_at": received_at,
                "status": status,
                "latency_ms": latency_ms,
                "represented_requests": represented_requests,
                "evidence_path": evidence_path,
            }
        )
        if trace_id is not UNSET:
            field_dict["trace_id"] = trace_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        telemetry_id = UUID(d.pop("telemetry_id"))

        received_at = datetime.datetime.fromisoformat(d.pop("received_at"))

        status = d.pop("status")

        latency_ms = d.pop("latency_ms")

        represented_requests = d.pop("represented_requests")

        evidence_path = d.pop("evidence_path")

        trace_id = d.pop("trace_id", UNSET)

        route_health_investigation_example = cls(
            telemetry_id=telemetry_id,
            received_at=received_at,
            status=status,
            latency_ms=latency_ms,
            represented_requests=represented_requests,
            evidence_path=evidence_path,
            trace_id=trace_id,
        )

        route_health_investigation_example.additional_properties = d
        return route_health_investigation_example

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
