from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_playbook_business_outcome_status import (
    OperationWorkflowPlaybookBusinessOutcomeStatus,
    check_operation_workflow_playbook_business_outcome_status,
)

if TYPE_CHECKING:
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

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        reason = self.reason

        evidence = self.evidence.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "status": status,
                "reason": reason,
                "evidence": evidence,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_readiness_business_outcome import OperationWorkflowReadinessBusinessOutcome

        d = dict(src_dict)
        status = check_operation_workflow_playbook_business_outcome_status(d.pop("status"))

        reason = d.pop("reason")

        evidence = OperationWorkflowReadinessBusinessOutcome.from_dict(d.pop("evidence"))

        operation_workflow_playbook_business_outcome = cls(
            status=status,
            reason=reason,
            evidence=evidence,
        )

        return operation_workflow_playbook_business_outcome
