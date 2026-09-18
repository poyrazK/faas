from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.debug_telemetry_span_dependency_type import (
    DebugTelemetrySpanDependencyType,
    check_debug_telemetry_span_dependency_type,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="DebugTelemetrySpan")


@_attrs_define
class DebugTelemetrySpan:
    """Bounded, redacted span evidence. DB statements are sanitized fingerprints."""

    trace_id: str
    span_id: str
    name: str
    kind: str
    duration_nanos: int
    parent_span_id: str | Unset = UNSET
    status: str | Unset = UNSET
    db_statement: str | Unset = UNSET
    """SQL fingerprint with literals redacted."""
    dependency_type: DebugTelemetrySpanDependencyType | Unset = UNSET
    """Allowlisted platform-owned dependency classification."""
    dependency_kind: str | Unset = UNSET
    """Allowlisted platform-owned dependency kind; raw span attributes are never returned."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        trace_id = self.trace_id

        span_id = self.span_id

        name = self.name

        kind = self.kind

        duration_nanos = self.duration_nanos

        parent_span_id = self.parent_span_id

        status = self.status

        db_statement = self.db_statement

        dependency_type: str | Unset = UNSET
        if not isinstance(self.dependency_type, Unset):
            dependency_type = self.dependency_type

        dependency_kind = self.dependency_kind

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "trace_id": trace_id,
                "span_id": span_id,
                "name": name,
                "kind": kind,
                "duration_nanos": duration_nanos,
            }
        )
        if parent_span_id is not UNSET:
            field_dict["parent_span_id"] = parent_span_id
        if status is not UNSET:
            field_dict["status"] = status
        if db_statement is not UNSET:
            field_dict["db_statement"] = db_statement
        if dependency_type is not UNSET:
            field_dict["dependency_type"] = dependency_type
        if dependency_kind is not UNSET:
            field_dict["dependency_kind"] = dependency_kind

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        trace_id = d.pop("trace_id")

        span_id = d.pop("span_id")

        name = d.pop("name")

        kind = d.pop("kind")

        duration_nanos = d.pop("duration_nanos")

        parent_span_id = d.pop("parent_span_id", UNSET)

        status = d.pop("status", UNSET)

        db_statement = d.pop("db_statement", UNSET)

        _dependency_type = d.pop("dependency_type", UNSET)
        dependency_type: DebugTelemetrySpanDependencyType | Unset
        if isinstance(_dependency_type, Unset):
            dependency_type = UNSET
        else:
            dependency_type = check_debug_telemetry_span_dependency_type(_dependency_type)

        dependency_kind = d.pop("dependency_kind", UNSET)

        debug_telemetry_span = cls(
            trace_id=trace_id,
            span_id=span_id,
            name=name,
            kind=kind,
            duration_nanos=duration_nanos,
            parent_span_id=parent_span_id,
            status=status,
            db_statement=db_statement,
            dependency_type=dependency_type,
            dependency_kind=dependency_kind,
        )

        debug_telemetry_span.additional_properties = d
        return debug_telemetry_span

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
