from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.debug_critical_path_span import DebugCriticalPathSpan


T = TypeVar("T", bound="DebugRequestCriticalPath")


@_attrs_define
class DebugRequestCriticalPath:
    """Bounded causal path reconstructed from retained span timing and parent links."""

    duration_ms: int
    complete: bool
    """True only when all retained spans have valid timing and parent links within the sample."""
    span_count: int
    spans: list[DebugCriticalPathSpan]
    slowest_span_id: str | Unset = UNSET
    slowest_span_name: str | Unset = UNSET
    slowest_exclusive_ms: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        duration_ms = self.duration_ms

        complete = self.complete

        span_count = self.span_count

        spans = []
        for spans_item_data in self.spans:
            spans_item = spans_item_data.to_dict()
            spans.append(spans_item)

        slowest_span_id = self.slowest_span_id

        slowest_span_name = self.slowest_span_name

        slowest_exclusive_ms = self.slowest_exclusive_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "duration_ms": duration_ms,
                "complete": complete,
                "span_count": span_count,
                "spans": spans,
            }
        )
        if slowest_span_id is not UNSET:
            field_dict["slowest_span_id"] = slowest_span_id
        if slowest_span_name is not UNSET:
            field_dict["slowest_span_name"] = slowest_span_name
        if slowest_exclusive_ms is not UNSET:
            field_dict["slowest_exclusive_ms"] = slowest_exclusive_ms

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.debug_critical_path_span import DebugCriticalPathSpan

        d = dict(src_dict)
        duration_ms = d.pop("duration_ms")

        complete = d.pop("complete")

        span_count = d.pop("span_count")

        spans = []
        _spans = d.pop("spans")
        for spans_item_data in _spans:
            spans_item = DebugCriticalPathSpan.from_dict(spans_item_data)

            spans.append(spans_item)

        slowest_span_id = d.pop("slowest_span_id", UNSET)

        slowest_span_name = d.pop("slowest_span_name", UNSET)

        slowest_exclusive_ms = d.pop("slowest_exclusive_ms", UNSET)

        debug_request_critical_path = cls(
            duration_ms=duration_ms,
            complete=complete,
            span_count=span_count,
            spans=spans,
            slowest_span_id=slowest_span_id,
            slowest_span_name=slowest_span_name,
            slowest_exclusive_ms=slowest_exclusive_ms,
        )

        debug_request_critical_path.additional_properties = d
        return debug_request_critical_path

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
