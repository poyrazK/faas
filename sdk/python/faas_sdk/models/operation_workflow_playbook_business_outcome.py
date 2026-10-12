from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_playbook_business_outcome_status import (
    OperationWorkflowPlaybookBusinessOutcomeStatus,
    check_operation_workflow_playbook_business_outcome_status,
)
from ..types import UNSET, Unset

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
    deadline_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowPlaybookBusinessOutcome.deadline_at. Pinned expected-result deadline, if configured."""
    deadline_reached: bool | Unset = UNSET
    """OperationWorkflowPlaybookBusinessOutcome.deadline_reached. The observation clock is at or past the deadline;
    does not establish failure or historical timeliness."""

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        reason = self.reason

        evidence = self.evidence.to_dict()

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
        if deadline_at is not UNSET:
            field_dict["deadline_at"] = deadline_at
        if deadline_reached is not UNSET:
            field_dict["deadline_reached"] = deadline_reached

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_readiness_business_outcome import OperationWorkflowReadinessBusinessOutcome

        d = dict(src_dict)
        status = check_operation_workflow_playbook_business_outcome_status(d.pop("status"))

        reason = d.pop("reason")

        evidence = OperationWorkflowReadinessBusinessOutcome.from_dict(d.pop("evidence"))

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
            deadline_at=deadline_at,
            deadline_reached=deadline_reached,
        )

        return operation_workflow_playbook_business_outcome
