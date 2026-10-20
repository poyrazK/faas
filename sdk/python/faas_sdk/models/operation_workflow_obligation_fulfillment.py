from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_obligation_fulfillment_action_kind import (
    OperationWorkflowObligationFulfillmentActionKind,
    check_operation_workflow_obligation_fulfillment_action_kind,
)

if TYPE_CHECKING:
    from ..models.operation_workflow_obligation_target_binding import OperationWorkflowObligationTargetBinding
    from ..models.operation_workflow_state_history_entry import OperationWorkflowStateHistoryEntry


T = TypeVar("T", bound="OperationWorkflowObligationFulfillment")


@_attrs_define
class OperationWorkflowObligationFulfillment:
    """Immutable human fulfillment receipt for one exact case and obligation cycle, linked to a retained application
    transition report. Does not close a shared source plan or assert business success or execution authority. Retained
    transition lookup scans at most 1000 exact-case reports.

    """

    binding: OperationWorkflowObligationTargetBinding
    """One obligation-cycle and exact-case target binding, recorded with an authenticated actor and reason.
    Unrelated edits preserve it; a new cycle never inherits it. Current readiness is independently reassessed."""
    reason: str
    action_kind: OperationWorkflowObligationFulfillmentActionKind
    owner: str
    evidence: OperationWorkflowStateHistoryEntry
    """One retained app-reported state update. Pages are ordered by revision, then stable publication and report
    identifiers."""
    evidence_checked_at: datetime.datetime
    completed_at: datetime.datetime
    completed_by: str

    def to_dict(self) -> dict[str, Any]:
        binding = self.binding.to_dict()

        reason = self.reason

        action_kind: str = self.action_kind

        owner = self.owner

        evidence = self.evidence.to_dict()

        evidence_checked_at = self.evidence_checked_at.isoformat()

        completed_at = self.completed_at.isoformat()

        completed_by = self.completed_by

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "binding": binding,
                "reason": reason,
                "action_kind": action_kind,
                "owner": owner,
                "evidence": evidence,
                "evidence_checked_at": evidence_checked_at,
                "completed_at": completed_at,
                "completed_by": completed_by,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_obligation_target_binding import OperationWorkflowObligationTargetBinding
        from ..models.operation_workflow_state_history_entry import OperationWorkflowStateHistoryEntry

        d = dict(src_dict)
        binding = OperationWorkflowObligationTargetBinding.from_dict(d.pop("binding"))

        reason = d.pop("reason")

        action_kind = check_operation_workflow_obligation_fulfillment_action_kind(d.pop("action_kind"))

        owner = d.pop("owner")

        evidence = OperationWorkflowStateHistoryEntry.from_dict(d.pop("evidence"))

        evidence_checked_at = datetime.datetime.fromisoformat(d.pop("evidence_checked_at"))

        completed_at = datetime.datetime.fromisoformat(d.pop("completed_at"))

        completed_by = d.pop("completed_by")

        operation_workflow_obligation_fulfillment = cls(
            binding=binding,
            reason=reason,
            action_kind=action_kind,
            owner=owner,
            evidence=evidence,
            evidence_checked_at=evidence_checked_at,
            completed_at=completed_at,
            completed_by=completed_by,
        )

        return operation_workflow_obligation_fulfillment
