from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_schedule_occurrence_response_status import (
    WorkflowScheduleOccurrenceResponseStatus,
    check_workflow_schedule_occurrence_response_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowScheduleOccurrenceResponse")


@_attrs_define
class WorkflowScheduleOccurrenceResponse:
    """Immutable admission outcome for one due schedule minute, retained independently from the workflow run."""

    id: UUID
    app_id: UUID
    workflow_name: str
    deployment_id: UUID
    scheduled_for: datetime.datetime
    evaluated_at: datetime.datetime
    status: WorkflowScheduleOccurrenceResponseStatus
    platform_tenant_id: UUID | Unset = UNSET
    run_id: UUID | Unset = UNSET
    """Present for started outcomes; retained after run expiry."""
    replay_run_id: UUID | Unset = UNSET
    """Present after a skipped occurrence has been replayed."""
    replayed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        workflow_name = self.workflow_name

        deployment_id = str(self.deployment_id)

        scheduled_for = self.scheduled_for.isoformat()

        evaluated_at = self.evaluated_at.isoformat()

        status: str = self.status

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        run_id: str | Unset = UNSET
        if not isinstance(self.run_id, Unset):
            run_id = str(self.run_id)

        replay_run_id: str | Unset = UNSET
        if not isinstance(self.replay_run_id, Unset):
            replay_run_id = str(self.replay_run_id)

        replayed_at: str | Unset = UNSET
        if not isinstance(self.replayed_at, Unset):
            replayed_at = self.replayed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "workflow_name": workflow_name,
                "deployment_id": deployment_id,
                "scheduled_for": scheduled_for,
                "evaluated_at": evaluated_at,
                "status": status,
            }
        )
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id
        if run_id is not UNSET:
            field_dict["run_id"] = run_id
        if replay_run_id is not UNSET:
            field_dict["replay_run_id"] = replay_run_id
        if replayed_at is not UNSET:
            field_dict["replayed_at"] = replayed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        workflow_name = d.pop("workflow_name")

        deployment_id = UUID(d.pop("deployment_id"))

        scheduled_for = datetime.datetime.fromisoformat(d.pop("scheduled_for"))

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        status = check_workflow_schedule_occurrence_response_status(d.pop("status"))

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        _run_id = d.pop("run_id", UNSET)
        run_id: UUID | Unset
        if isinstance(_run_id, Unset):
            run_id = UNSET
        else:
            run_id = UUID(_run_id)

        _replay_run_id = d.pop("replay_run_id", UNSET)
        replay_run_id: UUID | Unset
        if isinstance(_replay_run_id, Unset):
            replay_run_id = UNSET
        else:
            replay_run_id = UUID(_replay_run_id)

        _replayed_at = d.pop("replayed_at", UNSET)
        replayed_at: datetime.datetime | Unset
        if isinstance(_replayed_at, Unset):
            replayed_at = UNSET
        else:
            replayed_at = datetime.datetime.fromisoformat(_replayed_at)

        workflow_schedule_occurrence_response = cls(
            id=id,
            app_id=app_id,
            workflow_name=workflow_name,
            deployment_id=deployment_id,
            scheduled_for=scheduled_for,
            evaluated_at=evaluated_at,
            status=status,
            platform_tenant_id=platform_tenant_id,
            run_id=run_id,
            replay_run_id=replay_run_id,
            replayed_at=replayed_at,
        )

        workflow_schedule_occurrence_response.additional_properties = d
        return workflow_schedule_occurrence_response

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
