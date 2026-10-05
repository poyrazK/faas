from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

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
    result: Any | Unset = UNSET
    """Required typed output for succeeded."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        recovery_id = self.recovery_id

        expected_generation = self.expected_generation

        resolution: str = self.resolution

        evidence = self.evidence

        result = self.result

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "recovery_id": recovery_id,
                "expected_generation": expected_generation,
                "resolution": resolution,
                "evidence": evidence,
            }
        )
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

        result = d.pop("result", UNSET)

        operation_recovery_request = cls(
            recovery_id=recovery_id,
            expected_generation=expected_generation,
            resolution=resolution,
            evidence=evidence,
            result=result,
        )

        operation_recovery_request.additional_properties = d
        return operation_recovery_request

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
