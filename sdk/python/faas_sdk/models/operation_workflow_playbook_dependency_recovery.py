from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_playbook_dependency_state import OperationWorkflowPlaybookDependencyState
    from ..models.operation_workflow_readiness_outcome_escalation_reference import (
        OperationWorkflowReadinessOutcomeEscalationReference,
    )


T = TypeVar("T", bound="OperationWorkflowPlaybookDependencyRecovery")


@_attrs_define
class OperationWorkflowPlaybookDependencyRecovery:
    """OperationWorkflowPlaybookDependencyRecovery. Durable sampled waiting-to-ready observation for previously regressed
    steps, linked to the latest regression notice. Original reviews, queue assignments and clocks remain unchanged.

    """

    checked_at: datetime.datetime
    """OperationWorkflowPlaybookDependencyRecovery.checked_at. Successful case observation before revision-fenced
    publication."""
    regression: OperationWorkflowReadinessOutcomeEscalationReference
    """Reference to the single retained escalation notification in the same subscription. Pruning does not recreate
    escalations; links may expire."""
    ready_steps: list[OperationWorkflowPlaybookDependencyState]
    """OperationWorkflowPlaybookDependencyRecovery.ready_steps. Recovered subset grouped by current checklist owner
    and regression notice; completed reviews remain complete."""

    def to_dict(self) -> dict[str, Any]:
        checked_at = self.checked_at.isoformat()

        regression = self.regression.to_dict()

        ready_steps = []
        for ready_steps_item_data in self.ready_steps:
            ready_steps_item = ready_steps_item_data.to_dict()
            ready_steps.append(ready_steps_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "checked_at": checked_at,
                "regression": regression,
                "ready_steps": ready_steps,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_playbook_dependency_state import OperationWorkflowPlaybookDependencyState
        from ..models.operation_workflow_readiness_outcome_escalation_reference import (
            OperationWorkflowReadinessOutcomeEscalationReference,
        )

        d = dict(src_dict)
        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        regression = OperationWorkflowReadinessOutcomeEscalationReference.from_dict(d.pop("regression"))

        ready_steps = []
        _ready_steps = d.pop("ready_steps")
        for ready_steps_item_data in _ready_steps:
            ready_steps_item = OperationWorkflowPlaybookDependencyState.from_dict(ready_steps_item_data)

            ready_steps.append(ready_steps_item)

        operation_workflow_playbook_dependency_recovery = cls(
            checked_at=checked_at,
            regression=regression,
            ready_steps=ready_steps,
        )

        return operation_workflow_playbook_dependency_recovery
