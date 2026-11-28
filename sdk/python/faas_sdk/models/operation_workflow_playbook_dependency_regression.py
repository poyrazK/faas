from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_playbook_dependency_state import OperationWorkflowPlaybookDependencyState


T = TypeVar("T", bound="OperationWorkflowPlaybookDependencyRegression")


@_attrs_define
class OperationWorkflowPlaybookDependencyRegression:
    """OperationWorkflowPlaybookDependencyRegression. Durable sampled dependency regression grouped by affected checklist
    owner. Existing queue assignments, reviews and clocks remain unchanged.

    """

    checked_at: datetime.datetime
    """OperationWorkflowPlaybookDependencyRegression.checked_at. Successful case observation before revision-fenced
    publication."""
    reopened_prerequisites: list[OperationWorkflowPlaybookDependencyState]
    """OperationWorkflowPlaybookDependencyRegression.reopened_prerequisites. Relevant ancestors observed changing
    from completed to pending, including transitive causes."""
    affected_steps: list[OperationWorkflowPlaybookDependencyState]
    """OperationWorkflowPlaybookDependencyRegression.affected_steps. Downstream steps newly waiting, gaining
    blockers, or affected by a newly reopened ancestor; completed reviews retain a review suggestion."""

    def to_dict(self) -> dict[str, Any]:
        checked_at = self.checked_at.isoformat()

        reopened_prerequisites = []
        for reopened_prerequisites_item_data in self.reopened_prerequisites:
            reopened_prerequisites_item = reopened_prerequisites_item_data.to_dict()
            reopened_prerequisites.append(reopened_prerequisites_item)

        affected_steps = []
        for affected_steps_item_data in self.affected_steps:
            affected_steps_item = affected_steps_item_data.to_dict()
            affected_steps.append(affected_steps_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "checked_at": checked_at,
                "reopened_prerequisites": reopened_prerequisites,
                "affected_steps": affected_steps,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_playbook_dependency_state import OperationWorkflowPlaybookDependencyState

        d = dict(src_dict)
        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        reopened_prerequisites = []
        _reopened_prerequisites = d.pop("reopened_prerequisites")
        for reopened_prerequisites_item_data in _reopened_prerequisites:
            reopened_prerequisites_item = OperationWorkflowPlaybookDependencyState.from_dict(
                reopened_prerequisites_item_data
            )

            reopened_prerequisites.append(reopened_prerequisites_item)

        affected_steps = []
        _affected_steps = d.pop("affected_steps")
        for affected_steps_item_data in _affected_steps:
            affected_steps_item = OperationWorkflowPlaybookDependencyState.from_dict(affected_steps_item_data)

            affected_steps.append(affected_steps_item)

        operation_workflow_playbook_dependency_regression = cls(
            checked_at=checked_at,
            reopened_prerequisites=reopened_prerequisites,
            affected_steps=affected_steps,
        )

        return operation_workflow_playbook_dependency_regression
