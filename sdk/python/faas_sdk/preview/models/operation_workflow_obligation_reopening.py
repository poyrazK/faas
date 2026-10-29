from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_obligation_reopening_action_kind import (
    OperationWorkflowObligationReopeningActionKind,
    check_operation_workflow_obligation_reopening_action_kind,
)

if TYPE_CHECKING:
    from ..models.operation_workflow_obligation_target_binding import OperationWorkflowObligationTargetBinding


T = TypeVar("T", bound="OperationWorkflowObligationReopening")


@_attrs_define
class OperationWorkflowObligationReopening:
    """Immutable human decision to restore one current-source case/cycle to work, retaining the original fulfillment and
    its application evidence. Owner notices are delivered by the worker, with durable duplicate suppression. A
    subsequent fulfillment requires a report published after this reopening.

    """

    source_sequence: int
    """Source notification sequence within the delivering readiness subscription."""
    binding: OperationWorkflowObligationTargetBinding
    """One obligation-cycle and exact-case target binding, recorded with an authenticated actor and reason.
    Unrelated edits preserve it; a new cycle never inherits it. Current readiness is independently reassessed."""
    fulfilled_at: datetime.datetime
    reason: str
    action_kind: OperationWorkflowObligationReopeningActionKind
    owner: str
    reopened_at: datetime.datetime
    reopened_by: str

    def to_dict(self) -> dict[str, Any]:
        source_sequence = self.source_sequence

        binding = self.binding.to_dict()

        fulfilled_at = self.fulfilled_at.isoformat()

        reason = self.reason

        action_kind: str = self.action_kind

        owner = self.owner

        reopened_at = self.reopened_at.isoformat()

        reopened_by = self.reopened_by

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "source_sequence": source_sequence,
                "binding": binding,
                "fulfilled_at": fulfilled_at,
                "reason": reason,
                "action_kind": action_kind,
                "owner": owner,
                "reopened_at": reopened_at,
                "reopened_by": reopened_by,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_obligation_target_binding import OperationWorkflowObligationTargetBinding

        d = dict(src_dict)
        source_sequence = d.pop("source_sequence")

        binding = OperationWorkflowObligationTargetBinding.from_dict(d.pop("binding"))

        fulfilled_at = datetime.datetime.fromisoformat(d.pop("fulfilled_at"))

        reason = d.pop("reason")

        action_kind = check_operation_workflow_obligation_reopening_action_kind(d.pop("action_kind"))

        owner = d.pop("owner")

        reopened_at = datetime.datetime.fromisoformat(d.pop("reopened_at"))

        reopened_by = d.pop("reopened_by")

        operation_workflow_obligation_reopening = cls(
            source_sequence=source_sequence,
            binding=binding,
            fulfilled_at=fulfilled_at,
            reason=reason,
            action_kind=action_kind,
            owner=owner,
            reopened_at=reopened_at,
            reopened_by=reopened_by,
        )

        return operation_workflow_obligation_reopening
