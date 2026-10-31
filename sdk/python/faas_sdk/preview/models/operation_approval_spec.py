from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_approval_spec_approver import (
    OperationApprovalSpecApprover,
    check_operation_approval_spec_approver,
)

T = TypeVar("T", bound="OperationApprovalSpec")


@_attrs_define
class OperationApprovalSpec:
    """Declares a workflow callback step as an approval decided by the account or the operation's customer."""

    step: str
    approver: OperationApprovalSpecApprover

    def to_dict(self) -> dict[str, Any]:
        step = self.step

        approver: str = self.approver

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "step": step,
                "approver": approver,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        step = d.pop("step")

        approver = check_operation_approval_spec_approver(d.pop("approver"))

        operation_approval_spec = cls(
            step=step,
            approver=approver,
        )

        return operation_approval_spec
