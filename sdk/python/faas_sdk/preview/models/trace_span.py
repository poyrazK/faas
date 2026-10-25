from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.trace_span_status import TraceSpanStatus, check_trace_span_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.trace_span_attributes import TraceSpanAttributes


T = TypeVar("T", bound="TraceSpan")


@_attrs_define
class TraceSpan:
    """One span in a trace. Mirrors the OTel SDK's ReadOnlySpan
    attributes (kind, name, status, parent linkage, timing) so
    the customer's debug session can reconstruct the call tree.

    """

    trace_id: str
    span_id: str
    name: str
    parent_span_id: str | Unset = UNSET
    start_time: datetime.datetime | Unset = UNSET
    end_time: datetime.datetime | Unset = UNSET
    status: TraceSpanStatus | Unset = UNSET
    status_message: str | Unset = UNSET
    attributes: TraceSpanAttributes | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        trace_id = self.trace_id

        span_id = self.span_id

        name = self.name

        parent_span_id = self.parent_span_id

        start_time: str | Unset = UNSET
        if not isinstance(self.start_time, Unset):
            start_time = self.start_time.isoformat()

        end_time: str | Unset = UNSET
        if not isinstance(self.end_time, Unset):
            end_time = self.end_time.isoformat()

        status: str | Unset = UNSET
        if not isinstance(self.status, Unset):
            status = self.status

        status_message = self.status_message

        attributes: dict[str, Any] | Unset = UNSET
        if not isinstance(self.attributes, Unset):
            attributes = self.attributes.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "trace_id": trace_id,
                "span_id": span_id,
                "name": name,
            }
        )
        if parent_span_id is not UNSET:
            field_dict["parent_span_id"] = parent_span_id
        if start_time is not UNSET:
            field_dict["start_time"] = start_time
        if end_time is not UNSET:
            field_dict["end_time"] = end_time
        if status is not UNSET:
            field_dict["status"] = status
        if status_message is not UNSET:
            field_dict["status_message"] = status_message
        if attributes is not UNSET:
            field_dict["attributes"] = attributes

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.trace_span_attributes import TraceSpanAttributes

        d = dict(src_dict)
        trace_id = d.pop("trace_id")

        span_id = d.pop("span_id")

        name = d.pop("name")

        parent_span_id = d.pop("parent_span_id", UNSET)

        _start_time = d.pop("start_time", UNSET)
        start_time: datetime.datetime | Unset
        if isinstance(_start_time, Unset):
            start_time = UNSET
        else:
            start_time = datetime.datetime.fromisoformat(_start_time)

        _end_time = d.pop("end_time", UNSET)
        end_time: datetime.datetime | Unset
        if isinstance(_end_time, Unset):
            end_time = UNSET
        else:
            end_time = datetime.datetime.fromisoformat(_end_time)

        _status = d.pop("status", UNSET)
        status: TraceSpanStatus | Unset
        if isinstance(_status, Unset):
            status = UNSET
        else:
            status = check_trace_span_status(_status)

        status_message = d.pop("status_message", UNSET)

        _attributes = d.pop("attributes", UNSET)
        attributes: TraceSpanAttributes | Unset
        if isinstance(_attributes, Unset):
            attributes = UNSET
        else:
            attributes = TraceSpanAttributes.from_dict(_attributes)

        trace_span = cls(
            trace_id=trace_id,
            span_id=span_id,
            name=name,
            parent_span_id=parent_span_id,
            start_time=start_time,
            end_time=end_time,
            status=status,
            status_message=status_message,
            attributes=attributes,
        )

        trace_span.additional_properties = d
        return trace_span

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
