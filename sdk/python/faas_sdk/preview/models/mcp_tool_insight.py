from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="MCPToolInsight")


@_attrs_define
class MCPToolInsight:
    """One tool's calls, failures, argument mismatches, compute and latency."""

    tool: str
    calls: float
    errors: float
    cancellations: float
    mismatches: float
    """Calls whose arguments did not match the tool's input schema; also counted in errors."""
    execution_seconds: float
    gb_seconds: float
    estimated_millicents: int
    """Upper bound at the overage rate, in 1/100,000 USD."""
    p50_ms: float | Unset = UNSET
    p95_ms: float | Unset = UNSET
    p99_ms: float | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        tool = self.tool

        calls = self.calls

        errors = self.errors

        cancellations = self.cancellations

        mismatches = self.mismatches

        execution_seconds = self.execution_seconds

        gb_seconds = self.gb_seconds

        estimated_millicents = self.estimated_millicents

        p50_ms = self.p50_ms

        p95_ms = self.p95_ms

        p99_ms = self.p99_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "tool": tool,
                "calls": calls,
                "errors": errors,
                "cancellations": cancellations,
                "mismatches": mismatches,
                "execution_seconds": execution_seconds,
                "gb_seconds": gb_seconds,
                "estimated_millicents": estimated_millicents,
            }
        )
        if p50_ms is not UNSET:
            field_dict["p50_ms"] = p50_ms
        if p95_ms is not UNSET:
            field_dict["p95_ms"] = p95_ms
        if p99_ms is not UNSET:
            field_dict["p99_ms"] = p99_ms

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        tool = d.pop("tool")

        calls = d.pop("calls")

        errors = d.pop("errors")

        cancellations = d.pop("cancellations")

        mismatches = d.pop("mismatches")

        execution_seconds = d.pop("execution_seconds")

        gb_seconds = d.pop("gb_seconds")

        estimated_millicents = d.pop("estimated_millicents")

        p50_ms = d.pop("p50_ms", UNSET)

        p95_ms = d.pop("p95_ms", UNSET)

        p99_ms = d.pop("p99_ms", UNSET)

        mcp_tool_insight = cls(
            tool=tool,
            calls=calls,
            errors=errors,
            cancellations=cancellations,
            mismatches=mismatches,
            execution_seconds=execution_seconds,
            gb_seconds=gb_seconds,
            estimated_millicents=estimated_millicents,
            p50_ms=p50_ms,
            p95_ms=p95_ms,
            p99_ms=p99_ms,
        )

        mcp_tool_insight.additional_properties = d
        return mcp_tool_insight

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
