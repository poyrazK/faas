from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_condition_coverage_entry_status import (
    OperationWorkflowConditionCoverageEntryStatus,
    check_operation_workflow_condition_coverage_entry_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_business_condition_definition import OperationBusinessConditionDefinition
    from ..models.operation_workflow_decision_outcome_evidence import OperationWorkflowDecisionOutcomeEvidence


T = TypeVar("T", bound="OperationWorkflowConditionCoverageEntry")


@_attrs_define
class OperationWorkflowConditionCoverageEntry:
    """One condition expected in the current state. Match the declaring workflow step and exact condition ID and
    description within the current contract and latest explicit contexts. Evidence keeps the source report pinned age
    budget and decision identity. Reported includes any application result and may have no freshness budget; overdue is
    evidence past its budget. Missing has no evidence. This grants no authorization or verification.

    """

    step: str
    milestone: str
    definition: OperationBusinessConditionDefinition
    """Public condition vocabulary declared on a pinned workflow milestone step. A definition does not evaluate a
    predicate."""
    status: OperationWorkflowConditionCoverageEntryStatus
    evidence: OperationWorkflowDecisionOutcomeEvidence | Unset = UNSET
    """Retained report classifying this contributor. Completed outcome groups use the earliest qualifying fact;
    active condition groups use the latest matching fact among current decision contexts. The optional business
    decision snapshot references its fact within operation_id. A path without a decision is evidenced by the report
    itself. These are application-reported facts and do not establish causation."""

    def to_dict(self) -> dict[str, Any]:
        step = self.step

        milestone = self.milestone

        definition = self.definition.to_dict()

        status: str = self.status

        evidence: dict[str, Any] | Unset = UNSET
        if not isinstance(self.evidence, Unset):
            evidence = self.evidence.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "step": step,
                "milestone": milestone,
                "definition": definition,
                "status": status,
            }
        )
        if evidence is not UNSET:
            field_dict["evidence"] = evidence

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_business_condition_definition import OperationBusinessConditionDefinition
        from ..models.operation_workflow_decision_outcome_evidence import OperationWorkflowDecisionOutcomeEvidence

        d = dict(src_dict)
        step = d.pop("step")

        milestone = d.pop("milestone")

        definition = OperationBusinessConditionDefinition.from_dict(d.pop("definition"))

        status = check_operation_workflow_condition_coverage_entry_status(d.pop("status"))

        _evidence = d.pop("evidence", UNSET)
        evidence: OperationWorkflowDecisionOutcomeEvidence | Unset
        if isinstance(_evidence, Unset):
            evidence = UNSET
        else:
            evidence = OperationWorkflowDecisionOutcomeEvidence.from_dict(_evidence)

        operation_workflow_condition_coverage_entry = cls(
            step=step,
            milestone=milestone,
            definition=definition,
            status=status,
            evidence=evidence,
        )

        return operation_workflow_condition_coverage_entry
