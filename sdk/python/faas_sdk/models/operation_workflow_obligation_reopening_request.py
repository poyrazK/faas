from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_obligation_target_binding import OperationWorkflowObligationTargetBinding


T = TypeVar("T", bound="OperationWorkflowObligationReopeningRequest")


@_attrs_define
class OperationWorkflowObligationReopeningRequest:
    """Explicitly reopen the latest fulfillment for an exact bound case and current source cycle, with a reason. Preserve
    source status, owner, note and deadline; cannot combine with other action edits. Retired sources, superseded cycles
    and changed/cleared bindings cannot be reopened. Reopening does not execute an application transition.

    """

    binding: OperationWorkflowObligationTargetBinding
    """One obligation-cycle and exact-case target binding, recorded with an authenticated actor and reason.
    Unrelated edits preserve it; a new cycle never inherits it. Current readiness is independently reassessed."""
    fulfilled_at: datetime.datetime
    """Completion time of the exact latest fulfillment being reopened."""
    reason: str

    def to_dict(self) -> dict[str, Any]:
        binding = self.binding.to_dict()

        fulfilled_at = self.fulfilled_at.isoformat()

        reason = self.reason

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "binding": binding,
                "fulfilled_at": fulfilled_at,
                "reason": reason,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_obligation_target_binding import OperationWorkflowObligationTargetBinding

        d = dict(src_dict)
        binding = OperationWorkflowObligationTargetBinding.from_dict(d.pop("binding"))

        fulfilled_at = datetime.datetime.fromisoformat(d.pop("fulfilled_at"))

        reason = d.pop("reason")

        operation_workflow_obligation_reopening_request = cls(
            binding=binding,
            fulfilled_at=fulfilled_at,
            reason=reason,
        )

        return operation_workflow_obligation_reopening_request
