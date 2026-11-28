from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_revision_comparison_status import (
    OperationWorkflowRevisionComparisonStatus,
    check_operation_workflow_revision_comparison_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_change_reason import OperationWorkflowChangeReason
    from ..models.operation_workflow_condition_comparison import OperationWorkflowConditionComparison
    from ..models.operation_workflow_revision_change import OperationWorkflowRevisionChange
    from ..models.operation_workflow_revision_evidence import OperationWorkflowRevisionEvidence


T = TypeVar("T", bound="OperationWorkflowRevisionComparison")


@_attrs_define
class OperationWorkflowRevisionComparison:
    """Derived comparison with the preceding retained report on this history page in the same workflow run and tenant. Gaps
    and unavailable baselines prevent inferred changes or causal claims.

    """

    status: OperationWorkflowRevisionComparisonStatus
    summary: str
    changes: list[OperationWorkflowRevisionChange]
    evidence: list[OperationWorkflowRevisionEvidence]
    condition_comparison: OperationWorkflowConditionComparison | Unset = UNSET
    """Condition changes in explicit decision snapshots between consecutive retained revisions in the same tenant
    and workflow run and pinned contract. Shares history comparison statuses; missing decisions on metadata reports
    do not clear conditions. No causal inference."""
    change_reason: OperationWorkflowChangeReason | Unset = UNSET
    """Optional application-reported explanation tied to a business decision fact in the same transaction and
    Operation and workflow instance and pinned version. It describes application reasoning and grants no authority.
   """
    previous_report_id: UUID | Unset = UNSET
    previous_operation_id: UUID | Unset = UNSET
    previous_revision: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        summary = self.summary

        changes = []
        for changes_item_data in self.changes:
            changes_item = changes_item_data.to_dict()
            changes.append(changes_item)

        evidence = []
        for evidence_item_data in self.evidence:
            evidence_item = evidence_item_data.to_dict()
            evidence.append(evidence_item)

        condition_comparison: dict[str, Any] | Unset = UNSET
        if not isinstance(self.condition_comparison, Unset):
            condition_comparison = self.condition_comparison.to_dict()

        change_reason: dict[str, Any] | Unset = UNSET
        if not isinstance(self.change_reason, Unset):
            change_reason = self.change_reason.to_dict()

        previous_report_id: str | Unset = UNSET
        if not isinstance(self.previous_report_id, Unset):
            previous_report_id = str(self.previous_report_id)

        previous_operation_id: str | Unset = UNSET
        if not isinstance(self.previous_operation_id, Unset):
            previous_operation_id = str(self.previous_operation_id)

        previous_revision = self.previous_revision

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "status": status,
                "summary": summary,
                "changes": changes,
                "evidence": evidence,
            }
        )
        if condition_comparison is not UNSET:
            field_dict["condition_comparison"] = condition_comparison
        if change_reason is not UNSET:
            field_dict["change_reason"] = change_reason
        if previous_report_id is not UNSET:
            field_dict["previous_report_id"] = previous_report_id
        if previous_operation_id is not UNSET:
            field_dict["previous_operation_id"] = previous_operation_id
        if previous_revision is not UNSET:
            field_dict["previous_revision"] = previous_revision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_change_reason import OperationWorkflowChangeReason
        from ..models.operation_workflow_condition_comparison import OperationWorkflowConditionComparison
        from ..models.operation_workflow_revision_change import OperationWorkflowRevisionChange
        from ..models.operation_workflow_revision_evidence import OperationWorkflowRevisionEvidence

        d = dict(src_dict)
        status = check_operation_workflow_revision_comparison_status(d.pop("status"))

        summary = d.pop("summary")

        changes = []
        _changes = d.pop("changes")
        for changes_item_data in _changes:
            changes_item = OperationWorkflowRevisionChange.from_dict(changes_item_data)

            changes.append(changes_item)

        evidence = []
        _evidence = d.pop("evidence")
        for evidence_item_data in _evidence:
            evidence_item = OperationWorkflowRevisionEvidence.from_dict(evidence_item_data)

            evidence.append(evidence_item)

        _condition_comparison = d.pop("condition_comparison", UNSET)
        condition_comparison: OperationWorkflowConditionComparison | Unset
        if isinstance(_condition_comparison, Unset):
            condition_comparison = UNSET
        else:
            condition_comparison = OperationWorkflowConditionComparison.from_dict(_condition_comparison)

        _change_reason = d.pop("change_reason", UNSET)
        change_reason: OperationWorkflowChangeReason | Unset
        if isinstance(_change_reason, Unset):
            change_reason = UNSET
        else:
            change_reason = OperationWorkflowChangeReason.from_dict(_change_reason)

        _previous_report_id = d.pop("previous_report_id", UNSET)
        previous_report_id: UUID | Unset
        if isinstance(_previous_report_id, Unset):
            previous_report_id = UNSET
        else:
            previous_report_id = UUID(_previous_report_id)

        _previous_operation_id = d.pop("previous_operation_id", UNSET)
        previous_operation_id: UUID | Unset
        if isinstance(_previous_operation_id, Unset):
            previous_operation_id = UNSET
        else:
            previous_operation_id = UUID(_previous_operation_id)

        previous_revision = d.pop("previous_revision", UNSET)

        operation_workflow_revision_comparison = cls(
            status=status,
            summary=summary,
            changes=changes,
            evidence=evidence,
            condition_comparison=condition_comparison,
            change_reason=change_reason,
            previous_report_id=previous_report_id,
            previous_operation_id=previous_operation_id,
            previous_revision=previous_revision,
        )

        return operation_workflow_revision_comparison
