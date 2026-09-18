from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.debug_request_dependency_latency_type import (
    DebugRequestDependencyLatencyType,
    check_debug_request_dependency_latency_type,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="DebugRequestDependencyLatency")


@_attrs_define
class DebugRequestDependencyLatency:
    """Bounded latency aggregate for one retained dependency span group. Total duration may include overlapping child spans
    and is not a critical-path sum.

    """

    type_: DebugRequestDependencyLatencyType
    name: str
    calls: int
    total_duration_ms: int
    max_duration_ms: int
    kind: str | Unset = UNSET
    errors: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        type_: str = self.type_

        name = self.name

        calls = self.calls

        total_duration_ms = self.total_duration_ms

        max_duration_ms = self.max_duration_ms

        kind = self.kind

        errors = self.errors

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "type": type_,
                "name": name,
                "calls": calls,
                "total_duration_ms": total_duration_ms,
                "max_duration_ms": max_duration_ms,
            }
        )
        if kind is not UNSET:
            field_dict["kind"] = kind
        if errors is not UNSET:
            field_dict["errors"] = errors

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        type_ = check_debug_request_dependency_latency_type(d.pop("type"))

        name = d.pop("name")

        calls = d.pop("calls")

        total_duration_ms = d.pop("total_duration_ms")

        max_duration_ms = d.pop("max_duration_ms")

        kind = d.pop("kind", UNSET)

        errors = d.pop("errors", UNSET)

        debug_request_dependency_latency = cls(
            type_=type_,
            name=name,
            calls=calls,
            total_duration_ms=total_duration_ms,
            max_duration_ms=max_duration_ms,
            kind=kind,
            errors=errors,
        )

        debug_request_dependency_latency.additional_properties = d
        return debug_request_dependency_latency

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
