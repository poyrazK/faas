from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.debug_critical_path_span_dependency_type import (
    DebugCriticalPathSpanDependencyType,
    check_debug_critical_path_span_dependency_type,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="DebugCriticalPathSpan")


@_attrs_define
class DebugCriticalPathSpan:
    """One redacted span on the selected causal path. Exclusive time excludes overlapping direct children."""

    span_id: str
    name: str
    kind: str
    start_time: datetime.datetime
    end_time: datetime.datetime
    duration_ms: int
    exclusive_ms: int
    """Wall time not covered by overlapping direct child spans."""
    parent_span_id: str | Unset = UNSET
    dependency_type: DebugCriticalPathSpanDependencyType | Unset = UNSET
    dependency_kind: str | Unset = UNSET
    status: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        span_id = self.span_id

        name = self.name

        kind = self.kind

        start_time = self.start_time.isoformat()

        end_time = self.end_time.isoformat()

        duration_ms = self.duration_ms

        exclusive_ms = self.exclusive_ms

        parent_span_id = self.parent_span_id

        dependency_type: str | Unset = UNSET
        if not isinstance(self.dependency_type, Unset):
            dependency_type = self.dependency_type

        dependency_kind = self.dependency_kind

        status = self.status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "span_id": span_id,
                "name": name,
                "kind": kind,
                "start_time": start_time,
                "end_time": end_time,
                "duration_ms": duration_ms,
                "exclusive_ms": exclusive_ms,
            }
        )
        if parent_span_id is not UNSET:
            field_dict["parent_span_id"] = parent_span_id
        if dependency_type is not UNSET:
            field_dict["dependency_type"] = dependency_type
        if dependency_kind is not UNSET:
            field_dict["dependency_kind"] = dependency_kind
        if status is not UNSET:
            field_dict["status"] = status

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        span_id = d.pop("span_id")

        name = d.pop("name")

        kind = d.pop("kind")

        start_time = datetime.datetime.fromisoformat(d.pop("start_time"))

        end_time = datetime.datetime.fromisoformat(d.pop("end_time"))

        duration_ms = d.pop("duration_ms")

        exclusive_ms = d.pop("exclusive_ms")

        parent_span_id = d.pop("parent_span_id", UNSET)

        _dependency_type = d.pop("dependency_type", UNSET)
        dependency_type: DebugCriticalPathSpanDependencyType | Unset
        if isinstance(_dependency_type, Unset):
            dependency_type = UNSET
        else:
            dependency_type = check_debug_critical_path_span_dependency_type(_dependency_type)

        dependency_kind = d.pop("dependency_kind", UNSET)

        status = d.pop("status", UNSET)

        debug_critical_path_span = cls(
            span_id=span_id,
            name=name,
            kind=kind,
            start_time=start_time,
            end_time=end_time,
            duration_ms=duration_ms,
            exclusive_ms=exclusive_ms,
            parent_span_id=parent_span_id,
            dependency_type=dependency_type,
            dependency_kind=dependency_kind,
            status=status,
        )

        debug_critical_path_span.additional_properties = d
        return debug_critical_path_span

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
