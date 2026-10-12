from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_playbook_dependency_state_status import (
    OperationWorkflowPlaybookDependencyStateStatus,
    check_operation_workflow_playbook_dependency_state_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_playbook_step_readiness import OperationWorkflowPlaybookStepReadiness
    from ..models.operation_workflow_readiness_outcome_escalation_reference import (
        OperationWorkflowReadinessOutcomeEscalationReference,
    )


T = TypeVar("T", bound="OperationWorkflowPlaybookDependencyState")


@_attrs_define
class OperationWorkflowPlaybookDependencyState:
    """OperationWorkflowPlaybookDependencyState. One sampled human follow-up step observation, distinct from application
    reports and queue coordination.

    """

    outcome_code: str
    """OperationWorkflowPlaybookDependencyState.outcome_code. Exact pinned outcome list."""
    item_index: int
    """OperationWorkflowPlaybookDependencyState.item_index. Local immutable step index."""
    label: str
    """OperationWorkflowPlaybookDependencyState.label. Exact pinned step label."""
    status: OperationWorkflowPlaybookDependencyStateStatus
    """OperationWorkflowPlaybookDependencyState.status. Observed human review status."""
    owner: str
    """OperationWorkflowPlaybookDependencyState.owner. Current checklist step owner; inbox overrides remain
    separate."""
    readiness: OperationWorkflowPlaybookStepReadiness
    """OperationWorkflowPlaybookStepReadiness. Human review dependency status, not business state or execution
    authority."""
    regression: OperationWorkflowReadinessOutcomeEscalationReference | Unset = UNSET
    """Reference to the single retained escalation notification in the same subscription. Pruning does not recreate
    escalations; links may expire."""

    def to_dict(self) -> dict[str, Any]:
        outcome_code = self.outcome_code

        item_index = self.item_index

        label = self.label

        status: str = self.status

        owner = self.owner

        readiness = self.readiness.to_dict()

        regression: dict[str, Any] | Unset = UNSET
        if not isinstance(self.regression, Unset):
            regression = self.regression.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "outcome_code": outcome_code,
                "item_index": item_index,
                "label": label,
                "status": status,
                "owner": owner,
                "readiness": readiness,
            }
        )
        if regression is not UNSET:
            field_dict["regression"] = regression

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_playbook_step_readiness import OperationWorkflowPlaybookStepReadiness
        from ..models.operation_workflow_readiness_outcome_escalation_reference import (
            OperationWorkflowReadinessOutcomeEscalationReference,
        )

        d = dict(src_dict)
        outcome_code = d.pop("outcome_code")

        item_index = d.pop("item_index")

        label = d.pop("label")

        status = check_operation_workflow_playbook_dependency_state_status(d.pop("status"))

        owner = d.pop("owner")

        readiness = OperationWorkflowPlaybookStepReadiness.from_dict(d.pop("readiness"))

        _regression = d.pop("regression", UNSET)
        regression: OperationWorkflowReadinessOutcomeEscalationReference | Unset
        if isinstance(_regression, Unset):
            regression = UNSET
        else:
            regression = OperationWorkflowReadinessOutcomeEscalationReference.from_dict(_regression)

        operation_workflow_playbook_dependency_state = cls(
            outcome_code=outcome_code,
            item_index=item_index,
            label=label,
            status=status,
            owner=owner,
            readiness=readiness,
            regression=regression,
        )

        return operation_workflow_playbook_dependency_state
