from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_obligation_reaffirmation_action_kind import (
    OperationWorkflowObligationReaffirmationActionKind,
    check_operation_workflow_obligation_reaffirmation_action_kind,
)

if TYPE_CHECKING:
    from ..models.operation_workflow_obligation_target_binding import OperationWorkflowObligationTargetBinding
    from ..models.operation_workflow_state_history_entry import OperationWorkflowStateHistoryEntry


T = TypeVar("T", bound="OperationWorkflowObligationReaffirmation")


@_attrs_define
class OperationWorkflowObligationReaffirmation:
    """Immutable human review receipt with a fresh retained application state report. Original fulfillment and evidence
    remain intact; subsequent monitoring uses this proof as its basis. Does not authorize application execution or
    establish current business success. Each retained exact-case lookup scans at most 1000 reports.

    """

    action_kind: OperationWorkflowObligationReaffirmationActionKind
    binding: OperationWorkflowObligationTargetBinding
    """One obligation-cycle and exact-case target binding, recorded with an authenticated actor and reason.
    Unrelated edits preserve it; a new cycle never inherits it. Current readiness is independently reassessed."""
    fulfilled_at: datetime.datetime
    review_fingerprint: str
    reason: str
    source_sequence: int
    owner: str
    evidence: OperationWorkflowStateHistoryEntry
    """One retained app-reported state update. Pages are ordered by revision, then stable publication and report
    identifiers."""
    evidence_checked_at: datetime.datetime
    reaffirmed_at: datetime.datetime
    reaffirmed_by: str

    def to_dict(self) -> dict[str, Any]:
        action_kind: str = self.action_kind

        binding = self.binding.to_dict()

        fulfilled_at = self.fulfilled_at.isoformat()

        review_fingerprint = self.review_fingerprint

        reason = self.reason

        source_sequence = self.source_sequence

        owner = self.owner

        evidence = self.evidence.to_dict()

        evidence_checked_at = self.evidence_checked_at.isoformat()

        reaffirmed_at = self.reaffirmed_at.isoformat()

        reaffirmed_by = self.reaffirmed_by

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "action_kind": action_kind,
                "binding": binding,
                "fulfilled_at": fulfilled_at,
                "review_fingerprint": review_fingerprint,
                "reason": reason,
                "source_sequence": source_sequence,
                "owner": owner,
                "evidence": evidence,
                "evidence_checked_at": evidence_checked_at,
                "reaffirmed_at": reaffirmed_at,
                "reaffirmed_by": reaffirmed_by,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_obligation_target_binding import OperationWorkflowObligationTargetBinding
        from ..models.operation_workflow_state_history_entry import OperationWorkflowStateHistoryEntry

        d = dict(src_dict)
        action_kind = check_operation_workflow_obligation_reaffirmation_action_kind(d.pop("action_kind"))

        binding = OperationWorkflowObligationTargetBinding.from_dict(d.pop("binding"))

        fulfilled_at = datetime.datetime.fromisoformat(d.pop("fulfilled_at"))

        review_fingerprint = d.pop("review_fingerprint")

        reason = d.pop("reason")

        source_sequence = d.pop("source_sequence")

        owner = d.pop("owner")

        evidence = OperationWorkflowStateHistoryEntry.from_dict(d.pop("evidence"))

        evidence_checked_at = datetime.datetime.fromisoformat(d.pop("evidence_checked_at"))

        reaffirmed_at = datetime.datetime.fromisoformat(d.pop("reaffirmed_at"))

        reaffirmed_by = d.pop("reaffirmed_by")

        operation_workflow_obligation_reaffirmation = cls(
            action_kind=action_kind,
            binding=binding,
            fulfilled_at=fulfilled_at,
            review_fingerprint=review_fingerprint,
            reason=reason,
            source_sequence=source_sequence,
            owner=owner,
            evidence=evidence,
            evidence_checked_at=evidence_checked_at,
            reaffirmed_at=reaffirmed_at,
            reaffirmed_by=reaffirmed_by,
        )

        return operation_workflow_obligation_reaffirmation
