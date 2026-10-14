from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_playbook_completion_outcome_alignment import (
    OperationWorkflowPlaybookCompletionOutcomeAlignment,
    check_operation_workflow_playbook_completion_outcome_alignment,
)
from ..models.operation_workflow_playbook_completion_outcome_resolution import (
    OperationWorkflowPlaybookCompletionOutcomeResolution,
    check_operation_workflow_playbook_completion_outcome_resolution,
)

if TYPE_CHECKING:
    from ..models.operation_workflow_playbook_business_outcome import OperationWorkflowPlaybookBusinessOutcome


T = TypeVar("T", bound="OperationWorkflowPlaybookCompletionOutcome")


@_attrs_define
class OperationWorkflowPlaybookCompletionOutcome:
    """OperationWorkflowPlaybookCompletionOutcome. Immutable human disposition and exact scoped advisory application
    evidence observed before completion; neither establishes causation nor authorizes execution.

    """

    resolution: OperationWorkflowPlaybookCompletionOutcomeResolution
    """OperationWorkflowPlaybookCompletionOutcome.resolution. Human business disposition, distinct from application
    reports."""
    alignment: OperationWorkflowPlaybookCompletionOutcomeAlignment
    """OperationWorkflowPlaybookCompletionOutcome.alignment. Expected success matches achieved; accepted
    alternative matches a different terminal result permitted by an explicit pinned policy (legacy absence permits
    reviewer acceptance); cancellation makes no business assertion. Nonterminal evidence mismatches; incomparable
    evidence is unavailable."""
    explanation: str
    """OperationWorkflowPlaybookCompletionOutcome.explanation. Server explanation of disposition alignment; does
    not prevent human closure."""
    observation: OperationWorkflowPlaybookBusinessOutcome
    """OperationWorkflowPlaybookBusinessOutcome. Exact-case application business result compared with a frozen
    playbook expectation. Independent of human completion and evidence satisfaction; changed guidance never silently
    changes the expected outcome."""

    def to_dict(self) -> dict[str, Any]:
        resolution: str = self.resolution

        alignment: str = self.alignment

        explanation = self.explanation

        observation = self.observation.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "resolution": resolution,
                "alignment": alignment,
                "explanation": explanation,
                "observation": observation,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_playbook_business_outcome import OperationWorkflowPlaybookBusinessOutcome

        d = dict(src_dict)
        resolution = check_operation_workflow_playbook_completion_outcome_resolution(d.pop("resolution"))

        alignment = check_operation_workflow_playbook_completion_outcome_alignment(d.pop("alignment"))

        explanation = d.pop("explanation")

        observation = OperationWorkflowPlaybookBusinessOutcome.from_dict(d.pop("observation"))

        operation_workflow_playbook_completion_outcome = cls(
            resolution=resolution,
            alignment=alignment,
            explanation=explanation,
            observation=observation,
        )

        return operation_workflow_playbook_completion_outcome
