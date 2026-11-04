from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationUsage")


@_attrs_define
class OperationUsage:
    """What an Operation with limits has consumed. A running attempt is added when it ends."""

    execution_ms: int
    attempts: int

    def to_dict(self) -> dict[str, Any]:
        execution_ms = self.execution_ms

        attempts = self.attempts

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "execution_ms": execution_ms,
                "attempts": attempts,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        execution_ms = d.pop("execution_ms")

        attempts = d.pop("attempts")

        operation_usage = cls(
            execution_ms=execution_ms,
            attempts=attempts,
        )

        return operation_usage
