from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_approval_request_decision import (
    OperationApprovalRequestDecision,
    check_operation_approval_request_decision,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationApprovalRequest")


@_attrs_define
class OperationApprovalRequest:
    """One approval decision for the operation's current generation."""

    expected_generation: int
    decision: OperationApprovalRequestDecision
    comment: str | Unset = UNSET
    """Recorded with the decision in the operation history."""

    def to_dict(self) -> dict[str, Any]:
        expected_generation = self.expected_generation

        decision: str = self.decision

        comment = self.comment

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_generation": expected_generation,
                "decision": decision,
            }
        )
        if comment is not UNSET:
            field_dict["comment"] = comment

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_generation = d.pop("expected_generation")

        decision = check_operation_approval_request_decision(d.pop("decision"))

        comment = d.pop("comment", UNSET)

        operation_approval_request = cls(
            expected_generation=expected_generation,
            decision=decision,
            comment=comment,
        )

        return operation_approval_request
