from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_queued_run_cancel_outcome_outcome import (
    WorkflowQueuedRunCancelOutcomeOutcome,
    check_workflow_queued_run_cancel_outcome_outcome,
)
from ..models.workflow_queued_run_cancel_outcome_status import (
    WorkflowQueuedRunCancelOutcomeStatus,
    check_workflow_queued_run_cancel_outcome_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowQueuedRunCancelOutcome")


@_attrs_define
class WorkflowQueuedRunCancelOutcome:
    """Eligibility or result for cancelling one queued workflow run."""

    run_id: UUID
    outcome: WorkflowQueuedRunCancelOutcomeOutcome
    """Preview classification or the final result after the atomic cancellation recheck."""
    workflow_name: str | Unset = UNSET
    status: WorkflowQueuedRunCancelOutcomeStatus | Unset = UNSET
    started_at: datetime.datetime | Unset = UNSET
    scheduled_for: datetime.datetime | Unset = UNSET
    created_at: datetime.datetime | Unset = UNSET
    cancelled_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        run_id = str(self.run_id)

        outcome: str = self.outcome

        workflow_name = self.workflow_name

        status: str | Unset = UNSET
        if not isinstance(self.status, Unset):
            status = self.status

        started_at: str | Unset = UNSET
        if not isinstance(self.started_at, Unset):
            started_at = self.started_at.isoformat()

        scheduled_for: str | Unset = UNSET
        if not isinstance(self.scheduled_for, Unset):
            scheduled_for = self.scheduled_for.isoformat()

        created_at: str | Unset = UNSET
        if not isinstance(self.created_at, Unset):
            created_at = self.created_at.isoformat()

        cancelled_at: str | Unset = UNSET
        if not isinstance(self.cancelled_at, Unset):
            cancelled_at = self.cancelled_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "run_id": run_id,
                "outcome": outcome,
            }
        )
        if workflow_name is not UNSET:
            field_dict["workflow_name"] = workflow_name
        if status is not UNSET:
            field_dict["status"] = status
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if scheduled_for is not UNSET:
            field_dict["scheduled_for"] = scheduled_for
        if created_at is not UNSET:
            field_dict["created_at"] = created_at
        if cancelled_at is not UNSET:
            field_dict["cancelled_at"] = cancelled_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        run_id = UUID(d.pop("run_id"))

        outcome = check_workflow_queued_run_cancel_outcome_outcome(d.pop("outcome"))

        workflow_name = d.pop("workflow_name", UNSET)

        _status = d.pop("status", UNSET)
        status: WorkflowQueuedRunCancelOutcomeStatus | Unset
        if isinstance(_status, Unset):
            status = UNSET
        else:
            status = check_workflow_queued_run_cancel_outcome_status(_status)

        _started_at = d.pop("started_at", UNSET)
        started_at: datetime.datetime | Unset
        if isinstance(_started_at, Unset):
            started_at = UNSET
        else:
            started_at = datetime.datetime.fromisoformat(_started_at)

        _scheduled_for = d.pop("scheduled_for", UNSET)
        scheduled_for: datetime.datetime | Unset
        if isinstance(_scheduled_for, Unset):
            scheduled_for = UNSET
        else:
            scheduled_for = datetime.datetime.fromisoformat(_scheduled_for)

        _created_at = d.pop("created_at", UNSET)
        created_at: datetime.datetime | Unset
        if isinstance(_created_at, Unset):
            created_at = UNSET
        else:
            created_at = datetime.datetime.fromisoformat(_created_at)

        _cancelled_at = d.pop("cancelled_at", UNSET)
        cancelled_at: datetime.datetime | Unset
        if isinstance(_cancelled_at, Unset):
            cancelled_at = UNSET
        else:
            cancelled_at = datetime.datetime.fromisoformat(_cancelled_at)

        workflow_queued_run_cancel_outcome = cls(
            run_id=run_id,
            outcome=outcome,
            workflow_name=workflow_name,
            status=status,
            started_at=started_at,
            scheduled_for=scheduled_for,
            created_at=created_at,
            cancelled_at=cancelled_at,
        )

        workflow_queued_run_cancel_outcome.additional_properties = d
        return workflow_queued_run_cancel_outcome

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
