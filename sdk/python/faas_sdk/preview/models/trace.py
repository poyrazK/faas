from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.trace_span import TraceSpan


T = TypeVar("T", bound="Trace")


@_attrs_define
class Trace:
    """Issue #555. A single OpenTelemetry trace — the span tree for one
    request (one wake). The shape mirrors the SDK's ReadOnlySpan
    flattened to JSON: trace_id + span_id are W3C hex, attributes
    are a string map (the operator's debug session is a grep, not
    a query). The same trace_id may host a
    `gateway.handler` → `gateway.route` → `sched.wake` →
    `vmmd.create_*` → `guest.resume` → `guest.readiness` chain
    (issue #555 acceptance #1).

    """

    trace_id: str
    spans: list[TraceSpan]
    started_at: datetime.datetime | Unset = UNSET
    last_seen_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        trace_id = self.trace_id

        spans = []
        for spans_item_data in self.spans:
            spans_item = spans_item_data.to_dict()
            spans.append(spans_item)

        started_at: str | Unset = UNSET
        if not isinstance(self.started_at, Unset):
            started_at = self.started_at.isoformat()

        last_seen_at: str | Unset = UNSET
        if not isinstance(self.last_seen_at, Unset):
            last_seen_at = self.last_seen_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "trace_id": trace_id,
                "spans": spans,
            }
        )
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if last_seen_at is not UNSET:
            field_dict["last_seen_at"] = last_seen_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.trace_span import TraceSpan

        d = dict(src_dict)
        trace_id = d.pop("trace_id")

        spans = []
        _spans = d.pop("spans")
        for spans_item_data in _spans:
            spans_item = TraceSpan.from_dict(spans_item_data)

            spans.append(spans_item)

        _started_at = d.pop("started_at", UNSET)
        started_at: datetime.datetime | Unset
        if isinstance(_started_at, Unset):
            started_at = UNSET
        else:
            started_at = datetime.datetime.fromisoformat(_started_at)

        _last_seen_at = d.pop("last_seen_at", UNSET)
        last_seen_at: datetime.datetime | Unset
        if isinstance(_last_seen_at, Unset):
            last_seen_at = UNSET
        else:
            last_seen_at = datetime.datetime.fromisoformat(_last_seen_at)

        trace = cls(
            trace_id=trace_id,
            spans=spans,
            started_at=started_at,
            last_seen_at=last_seen_at,
        )

        trace.additional_properties = d
        return trace

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
