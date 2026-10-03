from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.issue_handoff_span import IssueHandoffSpan


T = TypeVar("T", bound="IssueHandoffRequest")


@_attrs_define
class IssueHandoffRequest:
    """Uniquely correlated singleton telemetry within the account's plan retention, with at most eight slowest sanitized
    spans. No arbitrary attributes or SQL statements are forwarded.

    """

    id: UUID
    route: str
    method: str
    status: int
    latency_ms: int
    cold_boot: bool
    received_at: datetime.datetime
    spans: list[IssueHandoffSpan]
    spans_truncated: bool
    trace_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        route = self.route

        method = self.method

        status = self.status

        latency_ms = self.latency_ms

        cold_boot = self.cold_boot

        received_at = self.received_at.isoformat()

        spans = []
        for spans_item_data in self.spans:
            spans_item = spans_item_data.to_dict()
            spans.append(spans_item)

        spans_truncated = self.spans_truncated

        trace_id = self.trace_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "route": route,
                "method": method,
                "status": status,
                "latency_ms": latency_ms,
                "cold_boot": cold_boot,
                "received_at": received_at,
                "spans": spans,
                "spans_truncated": spans_truncated,
            }
        )
        if trace_id is not UNSET:
            field_dict["trace_id"] = trace_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.issue_handoff_span import IssueHandoffSpan

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        route = d.pop("route")

        method = d.pop("method")

        status = d.pop("status")

        latency_ms = d.pop("latency_ms")

        cold_boot = d.pop("cold_boot")

        received_at = datetime.datetime.fromisoformat(d.pop("received_at"))

        spans = []
        _spans = d.pop("spans")
        for spans_item_data in _spans:
            spans_item = IssueHandoffSpan.from_dict(spans_item_data)

            spans.append(spans_item)

        spans_truncated = d.pop("spans_truncated")

        trace_id = d.pop("trace_id", UNSET)

        issue_handoff_request = cls(
            id=id,
            route=route,
            method=method,
            status=status,
            latency_ms=latency_ms,
            cold_boot=cold_boot,
            received_at=received_at,
            spans=spans,
            spans_truncated=spans_truncated,
            trace_id=trace_id,
        )

        issue_handoff_request.additional_properties = d
        return issue_handoff_request

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
