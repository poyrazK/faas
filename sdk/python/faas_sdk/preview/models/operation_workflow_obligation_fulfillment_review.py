from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_obligation_fulfillment_review_action_kind import (
    OperationWorkflowObligationFulfillmentReviewActionKind,
    check_operation_workflow_obligation_fulfillment_review_action_kind,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_obligation_target_binding import OperationWorkflowObligationTargetBinding


T = TypeVar("T", bound="OperationWorkflowObligationFulfillmentReview")


@_attrs_define
class OperationWorkflowObligationFulfillmentReview:
    """Bounded independent observation of the latest fulfilled case/cycle and its original or reaffirmed proof. Flags
    remain latched until explicit reaffirmation or reopening; normal workflow progress is not invalidation. Reasons
    describe retained coverage, not inferred business failure. Identical semantic observations suppress owner notices.

    """

    action_kind: OperationWorkflowObligationFulfillmentReviewActionKind
    binding: OperationWorkflowObligationTargetBinding
    """One obligation-cycle and exact-case target binding, recorded with an authenticated actor and reason.
    Unrelated edits preserve it; a new cycle never inherits it. Current readiness is independently reassessed."""
    fulfilled_at: datetime.datetime
    basis_at: datetime.datetime
    """Original fulfillment or latest explicit reaffirmation time; old observations cannot overwrite a newer basis."""
    source_sequence: int
    owner: str
    review_required: bool
    reasons: list[str]
    """Latched reasons requiring explicit review, retained even after current evidence recovers. Possible reasons
    are supporting_report_unavailable, current_evidence_unavailable, contract_changed, resolution_invalidated and
    verification_unavailable."""
    current_reasons: list[str]
    """Current observed issues; empty does not automatically clear a latched review flag."""
    checked_at: datetime.datetime
    fingerprint: str
    """Semantic comparison identity; timestamps and ordinary reporting revisions do not create new notices."""
    current_contract_version: int
    current_state_revision: int
    escalation_owner: str | Unset = UNSET
    """Optional escalation team, distinct from the review owner. Requires an explicit review deadline and positive
    grace period."""
    escalate_after_seconds: int | Unset = UNSET
    """Grace period after actual overdue-reminder delivery. Positive values require an escalation owner; zero
    disables escalation."""
    manual_owner: bool | Unset = UNSET
    """Explicit review-cycle owner, preserved across worker observations without altering original work ownership."""
    due_at: datetime.datetime | Unset = UNSET
    """Explicit review deadline, independent of the original source plan."""
    required_at: datetime.datetime | Unset = UNSET
    current_operation_id: UUID | Unset = UNSET
    current_report_id: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        action_kind: str = self.action_kind

        binding = self.binding.to_dict()

        fulfilled_at = self.fulfilled_at.isoformat()

        basis_at = self.basis_at.isoformat()

        source_sequence = self.source_sequence

        owner = self.owner

        review_required = self.review_required

        reasons = self.reasons

        current_reasons = self.current_reasons

        checked_at = self.checked_at.isoformat()

        fingerprint = self.fingerprint

        current_contract_version = self.current_contract_version

        current_state_revision = self.current_state_revision

        escalation_owner = self.escalation_owner

        escalate_after_seconds = self.escalate_after_seconds

        manual_owner = self.manual_owner

        due_at: str | Unset = UNSET
        if not isinstance(self.due_at, Unset):
            due_at = self.due_at.isoformat()

        required_at: str | Unset = UNSET
        if not isinstance(self.required_at, Unset):
            required_at = self.required_at.isoformat()

        current_operation_id: str | Unset = UNSET
        if not isinstance(self.current_operation_id, Unset):
            current_operation_id = str(self.current_operation_id)

        current_report_id = self.current_report_id

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "action_kind": action_kind,
                "binding": binding,
                "fulfilled_at": fulfilled_at,
                "basis_at": basis_at,
                "source_sequence": source_sequence,
                "owner": owner,
                "review_required": review_required,
                "reasons": reasons,
                "current_reasons": current_reasons,
                "checked_at": checked_at,
                "fingerprint": fingerprint,
                "current_contract_version": current_contract_version,
                "current_state_revision": current_state_revision,
            }
        )
        if escalation_owner is not UNSET:
            field_dict["escalation_owner"] = escalation_owner
        if escalate_after_seconds is not UNSET:
            field_dict["escalate_after_seconds"] = escalate_after_seconds
        if manual_owner is not UNSET:
            field_dict["manual_owner"] = manual_owner
        if due_at is not UNSET:
            field_dict["due_at"] = due_at
        if required_at is not UNSET:
            field_dict["required_at"] = required_at
        if current_operation_id is not UNSET:
            field_dict["current_operation_id"] = current_operation_id
        if current_report_id is not UNSET:
            field_dict["current_report_id"] = current_report_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_obligation_target_binding import OperationWorkflowObligationTargetBinding

        d = dict(src_dict)
        action_kind = check_operation_workflow_obligation_fulfillment_review_action_kind(d.pop("action_kind"))

        binding = OperationWorkflowObligationTargetBinding.from_dict(d.pop("binding"))

        fulfilled_at = datetime.datetime.fromisoformat(d.pop("fulfilled_at"))

        basis_at = datetime.datetime.fromisoformat(d.pop("basis_at"))

        source_sequence = d.pop("source_sequence")

        owner = d.pop("owner")

        review_required = d.pop("review_required")

        reasons = cast(list[str], d.pop("reasons"))

        current_reasons = cast(list[str], d.pop("current_reasons"))

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        fingerprint = d.pop("fingerprint")

        current_contract_version = d.pop("current_contract_version")

        current_state_revision = d.pop("current_state_revision")

        escalation_owner = d.pop("escalation_owner", UNSET)

        escalate_after_seconds = d.pop("escalate_after_seconds", UNSET)

        manual_owner = d.pop("manual_owner", UNSET)

        _due_at = d.pop("due_at", UNSET)
        due_at: datetime.datetime | Unset
        if isinstance(_due_at, Unset):
            due_at = UNSET
        else:
            due_at = datetime.datetime.fromisoformat(_due_at)

        _required_at = d.pop("required_at", UNSET)
        required_at: datetime.datetime | Unset
        if isinstance(_required_at, Unset):
            required_at = UNSET
        else:
            required_at = datetime.datetime.fromisoformat(_required_at)

        _current_operation_id = d.pop("current_operation_id", UNSET)
        current_operation_id: UUID | Unset
        if isinstance(_current_operation_id, Unset):
            current_operation_id = UNSET
        else:
            current_operation_id = UUID(_current_operation_id)

        current_report_id = d.pop("current_report_id", UNSET)

        operation_workflow_obligation_fulfillment_review = cls(
            action_kind=action_kind,
            binding=binding,
            fulfilled_at=fulfilled_at,
            basis_at=basis_at,
            source_sequence=source_sequence,
            owner=owner,
            review_required=review_required,
            reasons=reasons,
            current_reasons=current_reasons,
            checked_at=checked_at,
            fingerprint=fingerprint,
            current_contract_version=current_contract_version,
            current_state_revision=current_state_revision,
            escalation_owner=escalation_owner,
            escalate_after_seconds=escalate_after_seconds,
            manual_owner=manual_owner,
            due_at=due_at,
            required_at=required_at,
            current_operation_id=current_operation_id,
            current_report_id=current_report_id,
        )

        return operation_workflow_obligation_fulfillment_review
