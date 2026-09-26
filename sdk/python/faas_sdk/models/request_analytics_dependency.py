from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RequestAnalyticsDependency")


@_attrs_define
class RequestAnalyticsDependency:
    """Sampled aggregate of platform-classified dependency spans observed under one route. Exclusive duration subtracts
    overlapping direct child spans; it approximates dependency-owned wait and is not additive request latency. Calls are
    weighted by collapsed request count and are not a complete span call count.

    """

    type_: str
    """Allowlisted dependency category."""
    name: str
    """Redacted span name."""
    samples: int
    """Number of retained span observations."""
    calls: int
    """Observed span sample count weighted by the collapsed request-row count; not a full call count."""
    error_calls: int
    p95_ms: int
    """Weighted p95 inclusive span duration."""
    exclusive_p95_ms: int
    """Weighted p95 span duration excluding overlapping direct child spans."""
    kind: str | Unset = UNSET
    """Allowlisted dependency kind when available."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        type_ = self.type_

        name = self.name

        samples = self.samples

        calls = self.calls

        error_calls = self.error_calls

        p95_ms = self.p95_ms

        exclusive_p95_ms = self.exclusive_p95_ms

        kind = self.kind

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "type": type_,
                "name": name,
                "samples": samples,
                "calls": calls,
                "error_calls": error_calls,
                "p95_ms": p95_ms,
                "exclusive_p95_ms": exclusive_p95_ms,
            }
        )
        if kind is not UNSET:
            field_dict["kind"] = kind

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        type_ = d.pop("type")

        name = d.pop("name")

        samples = d.pop("samples")

        calls = d.pop("calls")

        error_calls = d.pop("error_calls")

        p95_ms = d.pop("p95_ms")

        exclusive_p95_ms = d.pop("exclusive_p95_ms")

        kind = d.pop("kind", UNSET)

        request_analytics_dependency = cls(
            type_=type_,
            name=name,
            samples=samples,
            calls=calls,
            error_calls=error_calls,
            p95_ms=p95_ms,
            exclusive_p95_ms=exclusive_p95_ms,
            kind=kind,
        )

        request_analytics_dependency.additional_properties = d
        return request_analytics_dependency

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
