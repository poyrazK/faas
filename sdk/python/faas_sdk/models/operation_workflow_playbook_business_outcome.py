from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_playbook_business_outcome_acceptance import (
    OperationWorkflowPlaybookBusinessOutcomeAcceptance,
    check_operation_workflow_playbook_business_outcome_acceptance,
)
from ..models.operation_workflow_playbook_business_outcome_status import (
    OperationWorkflowPlaybookBusinessOutcomeStatus,
    check_operation_workflow_playbook_business_outcome_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_playbook_outcome_acceptance_policy import (
        OperationWorkflowPlaybookOutcomeAcceptancePolicy,
    )
    from ..models.operation_workflow_playbook_outcome_guidance import OperationWorkflowPlaybookOutcomeGuidance
    from ..models.operation_workflow_readiness_business_outcome import OperationWorkflowReadinessBusinessOutcome


T = TypeVar("T", bound="OperationWorkflowPlaybookBusinessOutcome")


@_attrs_define
class OperationWorkflowPlaybookBusinessOutcome:
    """OperationWorkflowPlaybookBusinessOutcome. Exact-case application business result compared with a frozen playbook
    expectation. Independent of human completion and evidence satisfaction; changed guidance never silently changes the
    expected outcome.

    """

    status: OperationWorkflowPlaybookBusinessOutcomeStatus
    """OperationWorkflowPlaybookBusinessOutcome.status. Only a fresh comparable explicit terminal outcome
    establishes achieved or different outcome. Missing state, stale reports, missing terminal outcome and contract
    changes are unavailable."""
    reason: str
    """OperationWorkflowPlaybookBusinessOutcome.reason. Stable comparison reason; changing clocks and report
    revisions alone does not change the business result."""
    evidence: OperationWorkflowReadinessBusinessOutcome
    """Advisory comparison of the expected terminal outcome with a scoped retained application report. Missing
    state, stale reports, contract changes, older revisions and terminal reports without outcome codes remain
    unknown. Explicit outcome codes do not imply causation or authorize execution. Completion snapshots are
    immutable; current checks are live reads."""
    outcome_guidance: list[OperationWorkflowPlaybookOutcomeGuidance] | Unset = UNSET
    """OperationWorkflowPlaybookBusinessOutcome.outcome_guidance. Frozen complete guidance set retained for later
    exact-contract observations; never an execution instruction."""
    guidance: OperationWorkflowPlaybookOutcomeGuidance | Unset = UNSET
    """OperationWorkflowPlaybookOutcomeGuidance. Application-defined business meaning and optional suggested human
    follow-up, frozen with the playbook version. Plain text only; never establishes execution authority or changes
    outcome acceptance."""
    acceptance_policy: OperationWorkflowPlaybookOutcomeAcceptancePolicy | Unset = UNSET
    """OperationWorkflowPlaybookOutcomeAcceptancePolicy. Explicit business acceptance rules pinned with the
    playbook. Requires an expected outcome; an empty list permits no alternatives. Omission preserves legacy
    reviewer acceptance."""
    acceptance: OperationWorkflowPlaybookBusinessOutcomeAcceptance | Unset = UNSET
    """OperationWorkflowPlaybookBusinessOutcome.acceptance. Classification under the frozen explicit policy.
    Omitted for legacy playbooks; alternatives never change the primary expected-outcome status."""
    deadline_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowPlaybookBusinessOutcome.deadline_at. Pinned expected-result deadline, if configured."""
    deadline_reached: bool | Unset = UNSET
    """OperationWorkflowPlaybookBusinessOutcome.deadline_reached. The observation clock is at or past the deadline;
    does not establish failure or historical timeliness."""

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        reason = self.reason

        evidence = self.evidence.to_dict()

        outcome_guidance: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.outcome_guidance, Unset):
            outcome_guidance = []
            for outcome_guidance_item_data in self.outcome_guidance:
                outcome_guidance_item = outcome_guidance_item_data.to_dict()
                outcome_guidance.append(outcome_guidance_item)

        guidance: dict[str, Any] | Unset = UNSET
        if not isinstance(self.guidance, Unset):
            guidance = self.guidance.to_dict()

        acceptance_policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.acceptance_policy, Unset):
            acceptance_policy = self.acceptance_policy.to_dict()

        acceptance: str | Unset = UNSET
        if not isinstance(self.acceptance, Unset):
            acceptance = self.acceptance

        deadline_at: str | Unset = UNSET
        if not isinstance(self.deadline_at, Unset):
            deadline_at = self.deadline_at.isoformat()

        deadline_reached = self.deadline_reached

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "status": status,
                "reason": reason,
                "evidence": evidence,
            }
        )
        if outcome_guidance is not UNSET:
            field_dict["outcome_guidance"] = outcome_guidance
        if guidance is not UNSET:
            field_dict["guidance"] = guidance
        if acceptance_policy is not UNSET:
            field_dict["acceptance_policy"] = acceptance_policy
        if acceptance is not UNSET:
            field_dict["acceptance"] = acceptance
        if deadline_at is not UNSET:
            field_dict["deadline_at"] = deadline_at
        if deadline_reached is not UNSET:
            field_dict["deadline_reached"] = deadline_reached

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_playbook_outcome_acceptance_policy import (
            OperationWorkflowPlaybookOutcomeAcceptancePolicy,
        )
        from ..models.operation_workflow_playbook_outcome_guidance import OperationWorkflowPlaybookOutcomeGuidance
        from ..models.operation_workflow_readiness_business_outcome import OperationWorkflowReadinessBusinessOutcome

        d = dict(src_dict)
        status = check_operation_workflow_playbook_business_outcome_status(d.pop("status"))

        reason = d.pop("reason")

        evidence = OperationWorkflowReadinessBusinessOutcome.from_dict(d.pop("evidence"))

        _outcome_guidance = d.pop("outcome_guidance", UNSET)
        outcome_guidance: list[OperationWorkflowPlaybookOutcomeGuidance] | Unset = UNSET
        if _outcome_guidance is not UNSET:
            outcome_guidance = []
            for outcome_guidance_item_data in _outcome_guidance:
                outcome_guidance_item = OperationWorkflowPlaybookOutcomeGuidance.from_dict(outcome_guidance_item_data)

                outcome_guidance.append(outcome_guidance_item)

        _guidance = d.pop("guidance", UNSET)
        guidance: OperationWorkflowPlaybookOutcomeGuidance | Unset
        if isinstance(_guidance, Unset):
            guidance = UNSET
        else:
            guidance = OperationWorkflowPlaybookOutcomeGuidance.from_dict(_guidance)

        _acceptance_policy = d.pop("acceptance_policy", UNSET)
        acceptance_policy: OperationWorkflowPlaybookOutcomeAcceptancePolicy | Unset
        if isinstance(_acceptance_policy, Unset):
            acceptance_policy = UNSET
        else:
            acceptance_policy = OperationWorkflowPlaybookOutcomeAcceptancePolicy.from_dict(_acceptance_policy)

        _acceptance = d.pop("acceptance", UNSET)
        acceptance: OperationWorkflowPlaybookBusinessOutcomeAcceptance | Unset
        if isinstance(_acceptance, Unset):
            acceptance = UNSET
        else:
            acceptance = check_operation_workflow_playbook_business_outcome_acceptance(_acceptance)

        _deadline_at = d.pop("deadline_at", UNSET)
        deadline_at: datetime.datetime | Unset
        if isinstance(_deadline_at, Unset):
            deadline_at = UNSET
        else:
            deadline_at = datetime.datetime.fromisoformat(_deadline_at)

        deadline_reached = d.pop("deadline_reached", UNSET)

        operation_workflow_playbook_business_outcome = cls(
            status=status,
            reason=reason,
            evidence=evidence,
            outcome_guidance=outcome_guidance,
            guidance=guidance,
            acceptance_policy=acceptance_policy,
            acceptance=acceptance,
            deadline_at=deadline_at,
            deadline_reached=deadline_reached,
        )

        return operation_workflow_playbook_business_outcome
