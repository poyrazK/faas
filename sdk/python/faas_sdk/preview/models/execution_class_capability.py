from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.execution_class_capability_execution_class import (
    ExecutionClassCapabilityExecutionClass,
    check_execution_class_capability_execution_class,
)

T = TypeVar("T", bound="ExecutionClassCapability")


@_attrs_define
class ExecutionClassCapability:
    """Availability and admission limits for one execution class."""

    execution_class: ExecutionClassCapabilityExecutionClass
    default_timeout_ms: int
    max_timeout_ms: int
    max_concurrent_runs: int
    admission_available: bool
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        execution_class: str = self.execution_class

        default_timeout_ms = self.default_timeout_ms

        max_timeout_ms = self.max_timeout_ms

        max_concurrent_runs = self.max_concurrent_runs

        admission_available = self.admission_available

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "execution_class": execution_class,
                "default_timeout_ms": default_timeout_ms,
                "max_timeout_ms": max_timeout_ms,
                "max_concurrent_runs": max_concurrent_runs,
                "admission_available": admission_available,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        execution_class = check_execution_class_capability_execution_class(d.pop("execution_class"))

        default_timeout_ms = d.pop("default_timeout_ms")

        max_timeout_ms = d.pop("max_timeout_ms")

        max_concurrent_runs = d.pop("max_concurrent_runs")

        admission_available = d.pop("admission_available")

        execution_class_capability = cls(
            execution_class=execution_class,
            default_timeout_ms=default_timeout_ms,
            max_timeout_ms=max_timeout_ms,
            max_concurrent_runs=max_concurrent_runs,
            admission_available=admission_available,
        )

        execution_class_capability.additional_properties = d
        return execution_class_capability

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
