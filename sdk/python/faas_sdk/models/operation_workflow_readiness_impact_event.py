from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_impact_event_kind import (
    OperationWorkflowReadinessImpactEventKind,
    check_operation_workflow_readiness_impact_event_kind,
)
from ..models.operation_workflow_readiness_impact_event_reasons_item import (
    OperationWorkflowReadinessImpactEventReasonsItem,
    check_operation_workflow_readiness_impact_event_reasons_item,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_readiness_escalation_response import OperationWorkflowReadinessEscalationResponse
    from ..models.operation_workflow_readiness_impact_summary import OperationWorkflowReadinessImpactSummary


T = TypeVar("T", bound="OperationWorkflowReadinessImpactEvent")


@_attrs_define
class OperationWorkflowReadinessImpactEvent:
    """OperationWorkflowReadinessImpactEvent. Protected business result monitoring of retained unresolved human reviews in
    a scoped application environment; advisory confidence only.

    """

    workflow: str
    """OperationWorkflowReadinessImpactEvent.workflow. Exact workflow identity."""
    kind: OperationWorkflowReadinessImpactEventKind
    """OperationWorkflowReadinessImpactEvent.kind. Protected result category."""
    code: str
    """OperationWorkflowReadinessImpactEvent.code. Exact protected result slug."""
    checked_at: datetime.datetime
    """OperationWorkflowReadinessImpactEvent.checked_at. Advisory sample time."""
    reasons: list[OperationWorkflowReadinessImpactEventReasonsItem]
    """OperationWorkflowReadinessImpactEvent.reasons. First critical or overdue review transition, or source-
    confirmed clearance of tracked obligations; stable observations remain silent."""
    response: OperationWorkflowReadinessEscalationResponse | Unset = UNSET
    """Optional acknowledgment response target on a retained assigned escalation. Human acknowledgment fulfills it;
    completion or follow-up clearing stops it. A missed target emits one notice and optional guarded fallback.
    Assignment history retains the latest 16 changes."""
    previous: OperationWorkflowReadinessImpactSummary | Unset = UNSET
    """OperationWorkflowReadinessImpactSummary. Retained unresolved review confidence by workflow and protected
    business result, separate from reported business outcome."""
    current: OperationWorkflowReadinessImpactSummary | Unset = UNSET
    """OperationWorkflowReadinessImpactSummary. Retained unresolved review confidence by workflow and protected
    business result, separate from reported business outcome."""

    def to_dict(self) -> dict[str, Any]:
        workflow = self.workflow

        kind: str = self.kind

        code = self.code

        checked_at = self.checked_at.isoformat()

        reasons = []
        for reasons_item_data in self.reasons:
            reasons_item: str = reasons_item_data
            reasons.append(reasons_item)

        response: dict[str, Any] | Unset = UNSET
        if not isinstance(self.response, Unset):
            response = self.response.to_dict()

        previous: dict[str, Any] | Unset = UNSET
        if not isinstance(self.previous, Unset):
            previous = self.previous.to_dict()

        current: dict[str, Any] | Unset = UNSET
        if not isinstance(self.current, Unset):
            current = self.current.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "workflow": workflow,
                "kind": kind,
                "code": code,
                "checked_at": checked_at,
                "reasons": reasons,
            }
        )
        if response is not UNSET:
            field_dict["response"] = response
        if previous is not UNSET:
            field_dict["previous"] = previous
        if current is not UNSET:
            field_dict["current"] = current

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_readiness_escalation_response import (
            OperationWorkflowReadinessEscalationResponse,
        )
        from ..models.operation_workflow_readiness_impact_summary import OperationWorkflowReadinessImpactSummary

        d = dict(src_dict)
        workflow = d.pop("workflow")

        kind = check_operation_workflow_readiness_impact_event_kind(d.pop("kind"))

        code = d.pop("code")

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        reasons = []
        _reasons = d.pop("reasons")
        for reasons_item_data in _reasons:
            reasons_item = check_operation_workflow_readiness_impact_event_reasons_item(reasons_item_data)

            reasons.append(reasons_item)

        _response = d.pop("response", UNSET)
        response: OperationWorkflowReadinessEscalationResponse | Unset
        if isinstance(_response, Unset):
            response = UNSET
        else:
            response = OperationWorkflowReadinessEscalationResponse.from_dict(_response)

        _previous = d.pop("previous", UNSET)
        previous: OperationWorkflowReadinessImpactSummary | Unset
        if isinstance(_previous, Unset):
            previous = UNSET
        else:
            previous = OperationWorkflowReadinessImpactSummary.from_dict(_previous)

        _current = d.pop("current", UNSET)
        current: OperationWorkflowReadinessImpactSummary | Unset
        if isinstance(_current, Unset):
            current = UNSET
        else:
            current = OperationWorkflowReadinessImpactSummary.from_dict(_current)

        operation_workflow_readiness_impact_event = cls(
            workflow=workflow,
            kind=kind,
            code=code,
            checked_at=checked_at,
            reasons=reasons,
            response=response,
            previous=previous,
            current=current,
        )

        return operation_workflow_readiness_impact_event
