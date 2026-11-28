from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_schedule_preview_response_overlap import (
    WorkflowSchedulePreviewResponseOverlap,
    check_workflow_schedule_preview_response_overlap,
)

if TYPE_CHECKING:
    from ..models.workflow_schedule_catch_up_preview import WorkflowScheduleCatchUpPreview
    from ..models.workflow_schedule_dst_behavior import WorkflowScheduleDSTBehavior
    from ..models.workflow_schedule_fire_preview import WorkflowScheduleFirePreview


T = TypeVar("T", bound="WorkflowSchedulePreviewResponse")


@_attrs_define
class WorkflowSchedulePreviewResponse:
    """Read-only schedule simulation using the deployed trigger and durable cursor. Upcoming times do not guarantee
    capacity or execution.

    """

    workflow_name: str
    deployment_id: UUID
    schedule: str
    timezone: str
    """Effective IANA timezone."""
    overlap: WorkflowSchedulePreviewResponseOverlap
    """Configured overlap policy. Active runs may still block admission when overlap is skip."""
    enabled: bool
    observed_at: datetime.datetime
    evaluation_at: datetime.datetime
    """Actual or hypothetical evaluator time."""
    dst_behavior: WorkflowScheduleDSTBehavior
    """Gregale's timezone-transition behavior for fixed wall-time and interval cron expressions."""
    upcoming: list[WorkflowScheduleFirePreview]
    catch_up: WorkflowScheduleCatchUpPreview
    """Simulated next evaluator decision. It does not reserve quota or promise worker availability."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        workflow_name = self.workflow_name

        deployment_id = str(self.deployment_id)

        schedule = self.schedule

        timezone = self.timezone

        overlap: str = self.overlap

        enabled = self.enabled

        observed_at = self.observed_at.isoformat()

        evaluation_at = self.evaluation_at.isoformat()

        dst_behavior = self.dst_behavior.to_dict()

        upcoming = []
        for upcoming_item_data in self.upcoming:
            upcoming_item = upcoming_item_data.to_dict()
            upcoming.append(upcoming_item)

        catch_up = self.catch_up.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "workflow_name": workflow_name,
                "deployment_id": deployment_id,
                "schedule": schedule,
                "timezone": timezone,
                "overlap": overlap,
                "enabled": enabled,
                "observed_at": observed_at,
                "evaluation_at": evaluation_at,
                "dst_behavior": dst_behavior,
                "upcoming": upcoming,
                "catch_up": catch_up,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_schedule_catch_up_preview import WorkflowScheduleCatchUpPreview
        from ..models.workflow_schedule_dst_behavior import WorkflowScheduleDSTBehavior
        from ..models.workflow_schedule_fire_preview import WorkflowScheduleFirePreview

        d = dict(src_dict)
        workflow_name = d.pop("workflow_name")

        deployment_id = UUID(d.pop("deployment_id"))

        schedule = d.pop("schedule")

        timezone = d.pop("timezone")

        overlap = check_workflow_schedule_preview_response_overlap(d.pop("overlap"))

        enabled = d.pop("enabled")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        evaluation_at = datetime.datetime.fromisoformat(d.pop("evaluation_at"))

        dst_behavior = WorkflowScheduleDSTBehavior.from_dict(d.pop("dst_behavior"))

        upcoming = []
        _upcoming = d.pop("upcoming")
        for upcoming_item_data in _upcoming:
            upcoming_item = WorkflowScheduleFirePreview.from_dict(upcoming_item_data)

            upcoming.append(upcoming_item)

        catch_up = WorkflowScheduleCatchUpPreview.from_dict(d.pop("catch_up"))

        workflow_schedule_preview_response = cls(
            workflow_name=workflow_name,
            deployment_id=deployment_id,
            schedule=schedule,
            timezone=timezone,
            overlap=overlap,
            enabled=enabled,
            observed_at=observed_at,
            evaluation_at=evaluation_at,
            dst_behavior=dst_behavior,
            upcoming=upcoming,
            catch_up=catch_up,
        )

        workflow_schedule_preview_response.additional_properties = d
        return workflow_schedule_preview_response

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
