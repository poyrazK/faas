from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_recovery_preview_request_resolution import (
    OperationRecoveryPreviewRequestResolution,
    check_operation_recovery_preview_request_resolution,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationRecoveryPreviewRequest")


@_attrs_define
class OperationRecoveryPreviewRequest:
    """Proposed recovery resolution without a durable decision identity or reconciliation evidence."""

    expected_generation: int
    resolution: OperationRecoveryPreviewRequestResolution
    result: Any | Unset = UNSET
    """Proposed pinned-schema output required only for a succeeded preview."""

    def to_dict(self) -> dict[str, Any]:
        expected_generation = self.expected_generation

        resolution: str = self.resolution

        result = self.result

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_generation": expected_generation,
                "resolution": resolution,
            }
        )
        if result is not UNSET:
            field_dict["result"] = result

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_generation = d.pop("expected_generation")

        resolution = check_operation_recovery_preview_request_resolution(d.pop("resolution"))

        result = d.pop("result", UNSET)

        operation_recovery_preview_request = cls(
            expected_generation=expected_generation,
            resolution=resolution,
            result=result,
        )

        return operation_recovery_preview_request
