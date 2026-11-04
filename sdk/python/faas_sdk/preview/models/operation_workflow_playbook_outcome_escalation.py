from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_playbook_business_outcome import OperationWorkflowPlaybookBusinessOutcome
    from ..models.operation_workflow_readiness_escalation_response import OperationWorkflowReadinessEscalationResponse


T = TypeVar("T", bound="OperationWorkflowPlaybookOutcomeEscalation")


@_attrs_define
class OperationWorkflowPlaybookOutcomeEscalation:
    """OperationWorkflowPlaybookOutcomeEscalation. Retained assigned deadline follow-up evidence. Polling refreshes current
    observations and the first observed success only; acknowledgment, owner, notes, due times, reminders and completion
    remain human coordination.

    """

    watch_id: UUID
    """OperationWorkflowPlaybookOutcomeEscalation.watch_id. Exact durable watch identity for fenced evidence
    refresh."""
    progress_id: UUID
    """OperationWorkflowPlaybookOutcomeEscalation.progress_id. Exact pinned checklist that originally missed its
    observed deadline."""
    deadline_at: datetime.datetime
    """OperationWorkflowPlaybookOutcomeEscalation.deadline_at. Original outcome deadline, unchanged after delivery."""
    current: OperationWorkflowPlaybookBusinessOutcome
    """OperationWorkflowPlaybookBusinessOutcome. Exact-case application business result compared with a frozen
    playbook expectation. Independent of human completion and evidence satisfaction; changed guidance never silently
    changes the expected outcome."""
    response: OperationWorkflowReadinessEscalationResponse | Unset = UNSET
    """Optional acknowledgment response target on a retained assigned escalation. Human acknowledgment fulfills it;
    completion or follow-up clearing stops it. A missed target emits one notice and optional guarded fallback.
    Assignment history retains the latest 16 changes."""
    success_observed_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowPlaybookOutcomeEscalation.success_observed_at. First subsequent achieved observation,
    retained through corrections or evidence loss; does not prove when the business result occurred."""

    def to_dict(self) -> dict[str, Any]:
        watch_id = str(self.watch_id)

        progress_id = str(self.progress_id)

        deadline_at = self.deadline_at.isoformat()

        current = self.current.to_dict()

        response: dict[str, Any] | Unset = UNSET
        if not isinstance(self.response, Unset):
            response = self.response.to_dict()

        success_observed_at: str | Unset = UNSET
        if not isinstance(self.success_observed_at, Unset):
            success_observed_at = self.success_observed_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "watch_id": watch_id,
                "progress_id": progress_id,
                "deadline_at": deadline_at,
                "current": current,
            }
        )
        if response is not UNSET:
            field_dict["response"] = response
        if success_observed_at is not UNSET:
            field_dict["success_observed_at"] = success_observed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_playbook_business_outcome import OperationWorkflowPlaybookBusinessOutcome
        from ..models.operation_workflow_readiness_escalation_response import (
            OperationWorkflowReadinessEscalationResponse,
        )

        d = dict(src_dict)
        watch_id = UUID(d.pop("watch_id"))

        progress_id = UUID(d.pop("progress_id"))

        deadline_at = datetime.datetime.fromisoformat(d.pop("deadline_at"))

        current = OperationWorkflowPlaybookBusinessOutcome.from_dict(d.pop("current"))

        _response = d.pop("response", UNSET)
        response: OperationWorkflowReadinessEscalationResponse | Unset
        if isinstance(_response, Unset):
            response = UNSET
        else:
            response = OperationWorkflowReadinessEscalationResponse.from_dict(_response)

        _success_observed_at = d.pop("success_observed_at", UNSET)
        success_observed_at: datetime.datetime | Unset
        if isinstance(_success_observed_at, Unset):
            success_observed_at = UNSET
        else:
            success_observed_at = datetime.datetime.fromisoformat(_success_observed_at)

        operation_workflow_playbook_outcome_escalation = cls(
            watch_id=watch_id,
            progress_id=progress_id,
            deadline_at=deadline_at,
            current=current,
            response=response,
            success_observed_at=success_observed_at,
        )

        return operation_workflow_playbook_outcome_escalation
