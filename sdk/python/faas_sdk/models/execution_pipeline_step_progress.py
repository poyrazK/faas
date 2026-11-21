from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.execution_pipeline_step_progress_reason import (
    ExecutionPipelineStepProgressReason,
    check_execution_pipeline_step_progress_reason,
)
from ..models.execution_pipeline_step_progress_state import (
    ExecutionPipelineStepProgressState,
    check_execution_pipeline_step_progress_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ExecutionPipelineStepProgress")


@_attrs_define
class ExecutionPipelineStepProgress:
    """Current state of a named step and its latest attempt metadata."""

    label: str
    state: ExecutionPipelineStepProgressState
    reason: ExecutionPipelineStepProgressReason | Unset = UNSET
    attempt: int | Unset = UNSET
    run_id: UUID | Unset = UNSET
    retry_at: datetime.datetime | Unset = UNSET
    events_url: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        label = self.label

        state: str = self.state

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        attempt = self.attempt

        run_id: str | Unset = UNSET
        if not isinstance(self.run_id, Unset):
            run_id = str(self.run_id)

        retry_at: str | Unset = UNSET
        if not isinstance(self.retry_at, Unset):
            retry_at = self.retry_at.isoformat()

        events_url = self.events_url

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "label": label,
                "state": state,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if attempt is not UNSET:
            field_dict["attempt"] = attempt
        if run_id is not UNSET:
            field_dict["run_id"] = run_id
        if retry_at is not UNSET:
            field_dict["retry_at"] = retry_at
        if events_url is not UNSET:
            field_dict["events_url"] = events_url

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        label = d.pop("label")

        state = check_execution_pipeline_step_progress_state(d.pop("state"))

        _reason = d.pop("reason", UNSET)
        reason: ExecutionPipelineStepProgressReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_execution_pipeline_step_progress_reason(_reason)

        attempt = d.pop("attempt", UNSET)

        _run_id = d.pop("run_id", UNSET)
        run_id: UUID | Unset
        if isinstance(_run_id, Unset):
            run_id = UNSET
        else:
            run_id = UUID(_run_id)

        _retry_at = d.pop("retry_at", UNSET)
        retry_at: datetime.datetime | Unset
        if isinstance(_retry_at, Unset):
            retry_at = UNSET
        else:
            retry_at = datetime.datetime.fromisoformat(_retry_at)

        events_url = d.pop("events_url", UNSET)

        execution_pipeline_step_progress = cls(
            label=label,
            state=state,
            reason=reason,
            attempt=attempt,
            run_id=run_id,
            retry_at=retry_at,
            events_url=events_url,
        )

        execution_pipeline_step_progress.additional_properties = d
        return execution_pipeline_step_progress

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
