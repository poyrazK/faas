from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_recovery_request_resolution import (
    OperationRecoveryRequestResolution,
    check_operation_recovery_request_resolution,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationRecoveryRequest")


@_attrs_define
class OperationRecoveryRequest:
    """Explicit recovery decision with retained evidence and idempotent recovery identity."""

    recovery_id: str
    expected_generation: int
    resolution: OperationRecoveryRequestResolution
    evidence: str
    expected_inspection_revision: str | Unset = UNSET
    """Optional durable recovery inspection fence, rechecked before a new decision and ignored for an identical
    accepted receipt replay."""
    result: Any | Unset = UNSET
    """Required typed output for succeeded."""

    def to_dict(self) -> dict[str, Any]:
        recovery_id = self.recovery_id

        expected_generation = self.expected_generation

        resolution: str = self.resolution

        evidence = self.evidence

        expected_inspection_revision = self.expected_inspection_revision

        result = self.result

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "recovery_id": recovery_id,
                "expected_generation": expected_generation,
                "resolution": resolution,
                "evidence": evidence,
            }
        )
        if expected_inspection_revision is not UNSET:
            field_dict["expected_inspection_revision"] = expected_inspection_revision
        if result is not UNSET:
            field_dict["result"] = result

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        recovery_id = d.pop("recovery_id")

        expected_generation = d.pop("expected_generation")

        resolution = check_operation_recovery_request_resolution(d.pop("resolution"))

        evidence = d.pop("evidence")

        expected_inspection_revision = d.pop("expected_inspection_revision", UNSET)

        result = d.pop("result", UNSET)

        operation_recovery_request = cls(
            recovery_id=recovery_id,
            expected_generation=expected_generation,
            resolution=resolution,
            evidence=evidence,
            expected_inspection_revision=expected_inspection_revision,
            result=result,
        )

        return operation_recovery_request
