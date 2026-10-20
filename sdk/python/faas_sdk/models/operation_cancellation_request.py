from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationCancellationRequest")


@_attrs_define
class OperationCancellationRequest:
    """Compare-and-set cancellation intent for this generation."""

    expected_generation: int

    def to_dict(self) -> dict[str, Any]:
        expected_generation = self.expected_generation

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_generation": expected_generation,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_generation = d.pop("expected_generation")

        operation_cancellation_request = cls(
            expected_generation=expected_generation,
        )

        return operation_cancellation_request
