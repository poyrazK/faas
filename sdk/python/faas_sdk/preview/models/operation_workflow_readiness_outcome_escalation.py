from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_readiness_business_outcome import OperationWorkflowReadinessBusinessOutcome
    from ..models.operation_workflow_readiness_escalation_response import OperationWorkflowReadinessEscalationResponse


T = TypeVar("T", bound="OperationWorkflowReadinessOutcomeEscalation")


@_attrs_define
class OperationWorkflowReadinessOutcomeEscalation:
    """Live evidence for an assigned outcome escalation, separate from immutable deadline delivery evidence and human
    coordination. First observed application success is retained as a flag; it never automatically acknowledges or
    completes work. Monitoring ends with source retention.

    """

    watch_id: UUID
    """OperationWorkflowReadinessOutcomeEscalation.watch_id. Retained coordination escalation policy or evidence;
    business interpretation and execution remain application-owned."""
    source_notification_id: UUID
    """OperationWorkflowReadinessOutcomeEscalation.source_notification_id. Retained coordination escalation policy
    or evidence; business interpretation and execution remain application-owned."""
    source_sequence: int
    """OperationWorkflowReadinessOutcomeEscalation.source_sequence. Retained coordination escalation policy or
    evidence; business interpretation and execution remain application-owned."""
    current: OperationWorkflowReadinessBusinessOutcome
    """Advisory comparison of the expected terminal outcome with a scoped retained application report. Missing
    state, stale reports, contract changes, older revisions and terminal reports without outcome codes remain
    unknown. Explicit outcome codes do not imply causation or authorize execution. Completion snapshots are
    immutable; current checks are live reads."""
    response: OperationWorkflowReadinessEscalationResponse | Unset = UNSET
    """Optional acknowledgment response target on a retained assigned escalation. Human acknowledgment fulfills it;
    completion or follow-up clearing stops it. A missed target emits one notice and optional guarded fallback.
    Assignment history retains the latest 16 changes."""
    success_observed_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReadinessOutcomeEscalation.success_observed_at. Retained coordination escalation policy or
    evidence; business interpretation and execution remain application-owned."""

    def to_dict(self) -> dict[str, Any]:
        watch_id = str(self.watch_id)

        source_notification_id = str(self.source_notification_id)

        source_sequence = self.source_sequence

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
                "source_notification_id": source_notification_id,
                "source_sequence": source_sequence,
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
        from ..models.operation_workflow_readiness_business_outcome import OperationWorkflowReadinessBusinessOutcome
        from ..models.operation_workflow_readiness_escalation_response import (
            OperationWorkflowReadinessEscalationResponse,
        )

        d = dict(src_dict)
        watch_id = UUID(d.pop("watch_id"))

        source_notification_id = UUID(d.pop("source_notification_id"))

        source_sequence = d.pop("source_sequence")

        current = OperationWorkflowReadinessBusinessOutcome.from_dict(d.pop("current"))

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

        operation_workflow_readiness_outcome_escalation = cls(
            watch_id=watch_id,
            source_notification_id=source_notification_id,
            source_sequence=source_sequence,
            current=current,
            response=response,
            success_observed_at=success_observed_at,
        )

        return operation_workflow_readiness_outcome_escalation
