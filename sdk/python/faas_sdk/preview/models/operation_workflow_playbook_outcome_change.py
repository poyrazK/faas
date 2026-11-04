from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_playbook_business_outcome import OperationWorkflowPlaybookBusinessOutcome


T = TypeVar("T", bound="OperationWorkflowPlaybookOutcomeChange")


@_attrs_define
class OperationWorkflowPlaybookOutcomeChange:
    """OperationWorkflowPlaybookOutcomeChange. Saved before/after playbook result observation, separate from checklist
    review and business causation.

    """

    before_status: str
    """OperationWorkflowPlaybookOutcomeChange.before_status. Prior observed playbook business-result status."""
    before_reason: str
    """OperationWorkflowPlaybookOutcomeChange.before_reason. Prior comparison reason."""
    after: OperationWorkflowPlaybookBusinessOutcome
    """OperationWorkflowPlaybookBusinessOutcome. Exact-case application business result compared with a frozen
    playbook expectation. Independent of human completion and evidence satisfaction; changed guidance never silently
    changes the expected outcome."""
    before_reported_outcome_code: str | Unset = UNSET
    """OperationWorkflowPlaybookOutcomeChange.before_reported_outcome_code. Prior retained reported code, including
    diagnostic codes when comparability was unavailable."""

    def to_dict(self) -> dict[str, Any]:
        before_status = self.before_status

        before_reason = self.before_reason

        after = self.after.to_dict()

        before_reported_outcome_code = self.before_reported_outcome_code

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "before_status": before_status,
                "before_reason": before_reason,
                "after": after,
            }
        )
        if before_reported_outcome_code is not UNSET:
            field_dict["before_reported_outcome_code"] = before_reported_outcome_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_playbook_business_outcome import OperationWorkflowPlaybookBusinessOutcome

        d = dict(src_dict)
        before_status = d.pop("before_status")

        before_reason = d.pop("before_reason")

        after = OperationWorkflowPlaybookBusinessOutcome.from_dict(d.pop("after"))

        before_reported_outcome_code = d.pop("before_reported_outcome_code", UNSET)

        operation_workflow_playbook_outcome_change = cls(
            before_status=before_status,
            before_reason=before_reason,
            after=after,
            before_reported_outcome_code=before_reported_outcome_code,
        )

        return operation_workflow_playbook_outcome_change
