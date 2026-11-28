from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_playbook_business_outcome import OperationWorkflowPlaybookBusinessOutcome
    from ..models.operation_workflow_playbook_progress_item import OperationWorkflowPlaybookProgressItem


T = TypeVar("T", bound="OperationWorkflowPlaybookOutcomeFollowUp")


@_attrs_define
class OperationWorkflowPlaybookOutcomeFollowUp:
    """OperationWorkflowPlaybookOutcomeFollowUp. Human follow-up for one pinned reported outcome. First review captures an
    advisory pre-write business observation; later reviews do not rewrite it or assert execution success.

    """

    outcome_code: str
    """OperationWorkflowPlaybookOutcomeFollowUp.outcome_code. Exact pinned outcome associated with this list."""
    observation: OperationWorkflowPlaybookBusinessOutcome
    """OperationWorkflowPlaybookBusinessOutcome. Exact-case application business result compared with a frozen
    playbook expectation. Independent of human completion and evidence satisfaction; changed guidance never silently
    changes the expected outcome."""
    items: list[OperationWorkflowPlaybookProgressItem]
    """OperationWorkflowPlaybookOutcomeFollowUp.items. Pinned ordered steps with scoped evidence references and
    human review stamps."""
    completed_count: int
    """OperationWorkflowPlaybookOutcomeFollowUp.completed_count. Reviewed completed steps, independent of original
    checklist completion."""
    checklist_complete: bool
    """OperationWorkflowPlaybookOutcomeFollowUp.checklist_complete. All declared steps reviewed complete; never
    proves application state or execution."""

    def to_dict(self) -> dict[str, Any]:
        outcome_code = self.outcome_code

        observation = self.observation.to_dict()

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        completed_count = self.completed_count

        checklist_complete = self.checklist_complete

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "outcome_code": outcome_code,
                "observation": observation,
                "items": items,
                "completed_count": completed_count,
                "checklist_complete": checklist_complete,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_playbook_business_outcome import OperationWorkflowPlaybookBusinessOutcome
        from ..models.operation_workflow_playbook_progress_item import OperationWorkflowPlaybookProgressItem

        d = dict(src_dict)
        outcome_code = d.pop("outcome_code")

        observation = OperationWorkflowPlaybookBusinessOutcome.from_dict(d.pop("observation"))

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = OperationWorkflowPlaybookProgressItem.from_dict(items_item_data)

            items.append(items_item)

        completed_count = d.pop("completed_count")

        checklist_complete = d.pop("checklist_complete")

        operation_workflow_playbook_outcome_follow_up = cls(
            outcome_code=outcome_code,
            observation=observation,
            items=items,
            completed_count=completed_count,
            checklist_complete=checklist_complete,
        )

        return operation_workflow_playbook_outcome_follow_up
