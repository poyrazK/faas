from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_action_preview_response_reason import (
    OperationWorkflowActionPreviewResponseReason,
    check_operation_workflow_action_preview_response_reason,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_subject import OperationSubject
    from ..models.operation_workflow_state import OperationWorkflowState
    from ..models.operation_workflow_transition_readiness import OperationWorkflowTransitionReadiness


T = TypeVar("T", bound="OperationWorkflowActionPreviewResponse")


@_attrs_define
class OperationWorkflowActionPreviewResponse:
    """Observed current-state action candidates and their readiness without planned transaction evidence."""

    subject: OperationSubject
    """Immutable public business correlation metadata. Captured at admission and preserved through recovery and
    redeploy. Never an ownership or authorization claim."""
    workflow: str
    instance_id: str
    evaluated_at: datetime.datetime
    contract_version: int
    reason: OperationWorkflowActionPreviewResponseReason
    actions: list[OperationWorkflowTransitionReadiness]
    """Declared candidates evaluated with no planned milestones or decisions. Availability does not mean readiness
    or authorization."""
    action_count: int
    has_more: bool
    """The 100-action cap was reached. Filter by operation to narrow the preview; this endpoint has no cursor."""
    state: OperationWorkflowState | Unset = UNSET
    """Latest app-reported state for one declared workflow instance, including terminal and staleness indicators."""
    state_revision: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        subject = self.subject.to_dict()

        workflow = self.workflow

        instance_id = self.instance_id

        evaluated_at = self.evaluated_at.isoformat()

        contract_version = self.contract_version

        reason: str = self.reason

        actions = []
        for actions_item_data in self.actions:
            actions_item = actions_item_data.to_dict()
            actions.append(actions_item)

        action_count = self.action_count

        has_more = self.has_more

        state: dict[str, Any] | Unset = UNSET
        if not isinstance(self.state, Unset):
            state = self.state.to_dict()

        state_revision = self.state_revision

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subject": subject,
                "workflow": workflow,
                "instance_id": instance_id,
                "evaluated_at": evaluated_at,
                "contract_version": contract_version,
                "reason": reason,
                "actions": actions,
                "action_count": action_count,
                "has_more": has_more,
            }
        )
        if state is not UNSET:
            field_dict["state"] = state
        if state_revision is not UNSET:
            field_dict["state_revision"] = state_revision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_subject import OperationSubject
        from ..models.operation_workflow_state import OperationWorkflowState
        from ..models.operation_workflow_transition_readiness import OperationWorkflowTransitionReadiness

        d = dict(src_dict)
        subject = OperationSubject.from_dict(d.pop("subject"))

        workflow = d.pop("workflow")

        instance_id = d.pop("instance_id")

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        contract_version = d.pop("contract_version")

        reason = check_operation_workflow_action_preview_response_reason(d.pop("reason"))

        actions = []
        _actions = d.pop("actions")
        for actions_item_data in _actions:
            actions_item = OperationWorkflowTransitionReadiness.from_dict(actions_item_data)

            actions.append(actions_item)

        action_count = d.pop("action_count")

        has_more = d.pop("has_more")

        _state = d.pop("state", UNSET)
        state: OperationWorkflowState | Unset
        if isinstance(_state, Unset):
            state = UNSET
        else:
            state = OperationWorkflowState.from_dict(_state)

        state_revision = d.pop("state_revision", UNSET)

        operation_workflow_action_preview_response = cls(
            subject=subject,
            workflow=workflow,
            instance_id=instance_id,
            evaluated_at=evaluated_at,
            contract_version=contract_version,
            reason=reason,
            actions=actions,
            action_count=action_count,
            has_more=has_more,
            state=state,
            state_revision=state_revision,
        )

        return operation_workflow_action_preview_response
