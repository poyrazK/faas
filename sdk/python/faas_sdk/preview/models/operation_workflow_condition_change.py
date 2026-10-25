from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_condition_change_source import (
    OperationWorkflowConditionChangeSource,
    check_operation_workflow_condition_change_source,
)
from ..models.operation_workflow_condition_change_status import (
    OperationWorkflowConditionChangeStatus,
    check_operation_workflow_condition_change_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_business_condition_result import OperationBusinessConditionResult
    from ..models.operation_workflow_condition_evidence import OperationWorkflowConditionEvidence


T = TypeVar("T", bound="OperationWorkflowConditionChange")


@_attrs_define
class OperationWorkflowConditionChange:
    """Application condition snapshots matched by ID within the reason or selected path group on one pinned contract.
    Changed includes a changed result or description. Unreported means absent from an explicit current decision and does
    not prove resolution.

    """

    source: OperationWorkflowConditionChangeSource
    condition_id: str
    status: OperationWorkflowConditionChangeStatus
    current_evidence: OperationWorkflowConditionEvidence
    """Exact retained report and decision fact reference with reported rule identity. Previous evidence is absent
    when the baseline did not report a decision in this context. Links refer to the current retained Operation view.
   """
    explanation: str
    group_id: str | Unset = UNSET
    """Selected path group for path comparisons; omitted for reasons."""
    before: OperationBusinessConditionResult | Unset = UNSET
    """Application-reported condition result matching the pinned milestone declaration. Omitted conditions remain
    unreported; unknown is explicit. No execution authority or causal inference."""
    after: OperationBusinessConditionResult | Unset = UNSET
    """Application-reported condition result matching the pinned milestone declaration. Omitted conditions remain
    unreported; unknown is explicit. No execution authority or causal inference."""
    previous_evidence: OperationWorkflowConditionEvidence | Unset = UNSET
    """Exact retained report and decision fact reference with reported rule identity. Previous evidence is absent
    when the baseline did not report a decision in this context. Links refer to the current retained Operation view.
   """

    def to_dict(self) -> dict[str, Any]:
        source: str = self.source

        condition_id = self.condition_id

        status: str = self.status

        current_evidence = self.current_evidence.to_dict()

        explanation = self.explanation

        group_id = self.group_id

        before: dict[str, Any] | Unset = UNSET
        if not isinstance(self.before, Unset):
            before = self.before.to_dict()

        after: dict[str, Any] | Unset = UNSET
        if not isinstance(self.after, Unset):
            after = self.after.to_dict()

        previous_evidence: dict[str, Any] | Unset = UNSET
        if not isinstance(self.previous_evidence, Unset):
            previous_evidence = self.previous_evidence.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "source": source,
                "condition_id": condition_id,
                "status": status,
                "current_evidence": current_evidence,
                "explanation": explanation,
            }
        )
        if group_id is not UNSET:
            field_dict["group_id"] = group_id
        if before is not UNSET:
            field_dict["before"] = before
        if after is not UNSET:
            field_dict["after"] = after
        if previous_evidence is not UNSET:
            field_dict["previous_evidence"] = previous_evidence

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_business_condition_result import OperationBusinessConditionResult
        from ..models.operation_workflow_condition_evidence import OperationWorkflowConditionEvidence

        d = dict(src_dict)
        source = check_operation_workflow_condition_change_source(d.pop("source"))

        condition_id = d.pop("condition_id")

        status = check_operation_workflow_condition_change_status(d.pop("status"))

        current_evidence = OperationWorkflowConditionEvidence.from_dict(d.pop("current_evidence"))

        explanation = d.pop("explanation")

        group_id = d.pop("group_id", UNSET)

        _before = d.pop("before", UNSET)
        before: OperationBusinessConditionResult | Unset
        if isinstance(_before, Unset):
            before = UNSET
        else:
            before = OperationBusinessConditionResult.from_dict(_before)

        _after = d.pop("after", UNSET)
        after: OperationBusinessConditionResult | Unset
        if isinstance(_after, Unset):
            after = UNSET
        else:
            after = OperationBusinessConditionResult.from_dict(_after)

        _previous_evidence = d.pop("previous_evidence", UNSET)
        previous_evidence: OperationWorkflowConditionEvidence | Unset
        if isinstance(_previous_evidence, Unset):
            previous_evidence = UNSET
        else:
            previous_evidence = OperationWorkflowConditionEvidence.from_dict(_previous_evidence)

        operation_workflow_condition_change = cls(
            source=source,
            condition_id=condition_id,
            status=status,
            current_evidence=current_evidence,
            explanation=explanation,
            group_id=group_id,
            before=before,
            after=after,
            previous_evidence=previous_evidence,
        )

        return operation_workflow_condition_change
