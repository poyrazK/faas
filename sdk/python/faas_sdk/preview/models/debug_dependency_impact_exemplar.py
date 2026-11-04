from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.debug_dependency_impact_exemplar_window import (
    DebugDependencyImpactExemplarWindow,
    check_debug_dependency_impact_exemplar_window,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="DebugDependencyImpactExemplar")


@_attrs_define
class DebugDependencyImpactExemplar:
    """Bounded representative request for a historical dependency edge. Request identifiers resolve only to retained
    redacted debugger evidence.

    """

    request_id: str
    window: DebugDependencyImpactExemplarWindow
    received_at: datetime.datetime
    duration_ms: int
    http_status: int
    error: bool
    count: int
    trace_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        request_id = self.request_id

        window: str = self.window

        received_at = self.received_at.isoformat()

        duration_ms = self.duration_ms

        http_status = self.http_status

        error = self.error

        count = self.count

        trace_id = self.trace_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "request_id": request_id,
                "window": window,
                "received_at": received_at,
                "duration_ms": duration_ms,
                "http_status": http_status,
                "error": error,
                "count": count,
            }
        )
        if trace_id is not UNSET:
            field_dict["trace_id"] = trace_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = d.pop("request_id")

        window = check_debug_dependency_impact_exemplar_window(d.pop("window"))

        received_at = datetime.datetime.fromisoformat(d.pop("received_at"))

        duration_ms = d.pop("duration_ms")

        http_status = d.pop("http_status")

        error = d.pop("error")

        count = d.pop("count")

        trace_id = d.pop("trace_id", UNSET)

        debug_dependency_impact_exemplar = cls(
            request_id=request_id,
            window=window,
            received_at=received_at,
            duration_ms=duration_ms,
            http_status=http_status,
            error=error,
            count=count,
            trace_id=trace_id,
        )

        debug_dependency_impact_exemplar.additional_properties = d
        return debug_dependency_impact_exemplar

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
