from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_schedule_replay_outcome_outcome import (
    WorkflowScheduleReplayOutcomeOutcome,
    check_workflow_schedule_replay_outcome_outcome,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowScheduleReplayOutcome")


@_attrs_define
class WorkflowScheduleReplayOutcome:
    occurrence_id: UUID
    outcome: WorkflowScheduleReplayOutcomeOutcome
    platform_tenant_id: UUID | Unset = UNSET
    workflow_name: str | Unset = UNSET
    scheduled_for: datetime.datetime | Unset = UNSET
    replay_run_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        occurrence_id = str(self.occurrence_id)

        outcome: str = self.outcome

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        workflow_name = self.workflow_name

        scheduled_for: str | Unset = UNSET
        if not isinstance(self.scheduled_for, Unset):
            scheduled_for = self.scheduled_for.isoformat()

        replay_run_id: str | Unset = UNSET
        if not isinstance(self.replay_run_id, Unset):
            replay_run_id = str(self.replay_run_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "occurrence_id": occurrence_id,
                "outcome": outcome,
            }
        )
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id
        if workflow_name is not UNSET:
            field_dict["workflow_name"] = workflow_name
        if scheduled_for is not UNSET:
            field_dict["scheduled_for"] = scheduled_for
        if replay_run_id is not UNSET:
            field_dict["replay_run_id"] = replay_run_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        occurrence_id = UUID(d.pop("occurrence_id"))

        outcome = check_workflow_schedule_replay_outcome_outcome(d.pop("outcome"))

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        workflow_name = d.pop("workflow_name", UNSET)

        _scheduled_for = d.pop("scheduled_for", UNSET)
        scheduled_for: datetime.datetime | Unset
        if isinstance(_scheduled_for, Unset):
            scheduled_for = UNSET
        else:
            scheduled_for = datetime.datetime.fromisoformat(_scheduled_for)

        _replay_run_id = d.pop("replay_run_id", UNSET)
        replay_run_id: UUID | Unset
        if isinstance(_replay_run_id, Unset):
            replay_run_id = UNSET
        else:
            replay_run_id = UUID(_replay_run_id)

        workflow_schedule_replay_outcome = cls(
            occurrence_id=occurrence_id,
            outcome=outcome,
            platform_tenant_id=platform_tenant_id,
            workflow_name=workflow_name,
            scheduled_for=scheduled_for,
            replay_run_id=replay_run_id,
        )

        workflow_schedule_replay_outcome.additional_properties = d
        return workflow_schedule_replay_outcome

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
