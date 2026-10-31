from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowObligationTransitionTarget")


@_attrs_define
class OperationWorkflowObligationTransitionTarget:
    """Exact declared transition identity and pinned workflow contract version; it is an advisory target, not execution
    authorization.

    """

    operation: str
    from_: str
    to: str
    contract_version: int

    def to_dict(self) -> dict[str, Any]:
        operation = self.operation

        from_ = self.from_

        to = self.to

        contract_version = self.contract_version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "operation": operation,
                "from": from_,
                "to": to,
                "contract_version": contract_version,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        operation = d.pop("operation")

        from_ = d.pop("from")

        to = d.pop("to")

        contract_version = d.pop("contract_version")

        operation_workflow_obligation_transition_target = cls(
            operation=operation,
            from_=from_,
            to=to,
            contract_version=contract_version,
        )

        return operation_workflow_obligation_transition_target
