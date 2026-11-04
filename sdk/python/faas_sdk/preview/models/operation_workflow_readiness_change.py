from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_change_added_reasons_item import (
    OperationWorkflowReadinessChangeAddedReasonsItem,
    check_operation_workflow_readiness_change_added_reasons_item,
)
from ..models.operation_workflow_readiness_change_changed_categories_item import (
    OperationWorkflowReadinessChangeChangedCategoriesItem,
    check_operation_workflow_readiness_change_changed_categories_item,
)
from ..models.operation_workflow_readiness_change_resolved_reasons_item import (
    OperationWorkflowReadinessChangeResolvedReasonsItem,
    check_operation_workflow_readiness_change_resolved_reasons_item,
)
from ..models.operation_workflow_readiness_change_status import (
    OperationWorkflowReadinessChangeStatus,
    check_operation_workflow_readiness_change_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_readiness_evidence_change import OperationWorkflowReadinessEvidenceChange
    from ..models.operation_workflow_readiness_queue_entry import OperationWorkflowReadinessQueueEntry
    from ..models.operation_workflow_readiness_snapshot_entry import OperationWorkflowReadinessSnapshotEntry


T = TypeVar("T", bound="OperationWorkflowReadinessChange")


@_attrs_define
class OperationWorkflowReadinessChange:
    """Readiness comparison of the saved edge with fresh retained evidence. Contract and state boundaries are explicit."""

    baseline: OperationWorkflowReadinessSnapshotEntry
    """One saved transition identity and stable evidence summaries for comparison within the same ownership scope."""
    status: OperationWorkflowReadinessChangeStatus
    """ReadinessChange. Classification; only comparable unchanged requirements can become newly ready or newly
    blocked."""
    added_reasons: list[OperationWorkflowReadinessChangeAddedReasonsItem]
    """ReadinessChange. Declared requirement codes now unmet that were absent from the saved reasons."""
    resolved_reasons: list[OperationWorkflowReadinessChangeResolvedReasonsItem]
    """ReadinessChange. Saved requirement codes that are no longer unmet; this does not prove application
    authorization."""
    changed_categories: list[OperationWorkflowReadinessChangeChangedCategoriesItem]
    """ReadinessChange. Evidence categories whose complete stable summaries changed."""
    explanation: str
    """ReadinessChange. Human-readable meaning and comparison boundary."""
    evidence_changes: list[OperationWorkflowReadinessEvidenceChange]
    """Individual retained evidence changes; missing verification is never inferred fulfilled."""
    evidence_comparison_complete: bool
    """True only when both evidence inventories are complete and requirements remain comparable."""
    current_operation_id: UUID | Unset = UNSET
    """ReadinessChange. Latest retained reporting Operation when current state is available."""
    current_state: str | Unset = UNSET
    """ReadinessChange. Current retained workflow state even when the saved edge is no longer applicable."""
    current: OperationWorkflowReadinessQueueEntry | Unset = UNSET
    """One declared current-state transition evaluated without planned evidence; reporting is advisory."""

    def to_dict(self) -> dict[str, Any]:
        baseline = self.baseline.to_dict()

        status: str = self.status

        added_reasons = []
        for added_reasons_item_data in self.added_reasons:
            added_reasons_item: str = added_reasons_item_data
            added_reasons.append(added_reasons_item)

        resolved_reasons = []
        for resolved_reasons_item_data in self.resolved_reasons:
            resolved_reasons_item: str = resolved_reasons_item_data
            resolved_reasons.append(resolved_reasons_item)

        changed_categories = []
        for changed_categories_item_data in self.changed_categories:
            changed_categories_item: str = changed_categories_item_data
            changed_categories.append(changed_categories_item)

        explanation = self.explanation

        evidence_changes = []
        for evidence_changes_item_data in self.evidence_changes:
            evidence_changes_item = evidence_changes_item_data.to_dict()
            evidence_changes.append(evidence_changes_item)

        evidence_comparison_complete = self.evidence_comparison_complete

        current_operation_id: str | Unset = UNSET
        if not isinstance(self.current_operation_id, Unset):
            current_operation_id = str(self.current_operation_id)

        current_state = self.current_state

        current: dict[str, Any] | Unset = UNSET
        if not isinstance(self.current, Unset):
            current = self.current.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "baseline": baseline,
                "status": status,
                "added_reasons": added_reasons,
                "resolved_reasons": resolved_reasons,
                "changed_categories": changed_categories,
                "explanation": explanation,
                "evidence_changes": evidence_changes,
                "evidence_comparison_complete": evidence_comparison_complete,
            }
        )
        if current_operation_id is not UNSET:
            field_dict["current_operation_id"] = current_operation_id
        if current_state is not UNSET:
            field_dict["current_state"] = current_state
        if current is not UNSET:
            field_dict["current"] = current

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_readiness_evidence_change import OperationWorkflowReadinessEvidenceChange
        from ..models.operation_workflow_readiness_queue_entry import OperationWorkflowReadinessQueueEntry
        from ..models.operation_workflow_readiness_snapshot_entry import OperationWorkflowReadinessSnapshotEntry

        d = dict(src_dict)
        baseline = OperationWorkflowReadinessSnapshotEntry.from_dict(d.pop("baseline"))

        status = check_operation_workflow_readiness_change_status(d.pop("status"))

        added_reasons = []
        _added_reasons = d.pop("added_reasons")
        for added_reasons_item_data in _added_reasons:
            added_reasons_item = check_operation_workflow_readiness_change_added_reasons_item(added_reasons_item_data)

            added_reasons.append(added_reasons_item)

        resolved_reasons = []
        _resolved_reasons = d.pop("resolved_reasons")
        for resolved_reasons_item_data in _resolved_reasons:
            resolved_reasons_item = check_operation_workflow_readiness_change_resolved_reasons_item(
                resolved_reasons_item_data
            )

            resolved_reasons.append(resolved_reasons_item)

        changed_categories = []
        _changed_categories = d.pop("changed_categories")
        for changed_categories_item_data in _changed_categories:
            changed_categories_item = check_operation_workflow_readiness_change_changed_categories_item(
                changed_categories_item_data
            )

            changed_categories.append(changed_categories_item)

        explanation = d.pop("explanation")

        evidence_changes = []
        _evidence_changes = d.pop("evidence_changes")
        for evidence_changes_item_data in _evidence_changes:
            evidence_changes_item = OperationWorkflowReadinessEvidenceChange.from_dict(evidence_changes_item_data)

            evidence_changes.append(evidence_changes_item)

        evidence_comparison_complete = d.pop("evidence_comparison_complete")

        _current_operation_id = d.pop("current_operation_id", UNSET)
        current_operation_id: UUID | Unset
        if isinstance(_current_operation_id, Unset):
            current_operation_id = UNSET
        else:
            current_operation_id = UUID(_current_operation_id)

        current_state = d.pop("current_state", UNSET)

        _current = d.pop("current", UNSET)
        current: OperationWorkflowReadinessQueueEntry | Unset
        if isinstance(_current, Unset):
            current = UNSET
        else:
            current = OperationWorkflowReadinessQueueEntry.from_dict(_current)

        operation_workflow_readiness_change = cls(
            baseline=baseline,
            status=status,
            added_reasons=added_reasons,
            resolved_reasons=resolved_reasons,
            changed_categories=changed_categories,
            explanation=explanation,
            evidence_changes=evidence_changes,
            evidence_comparison_complete=evidence_comparison_complete,
            current_operation_id=current_operation_id,
            current_state=current_state,
            current=current,
        )

        return operation_workflow_readiness_change
