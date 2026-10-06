from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.workflow_trigger_spec_overlap import WorkflowTriggerSpecOverlap, check_workflow_trigger_spec_overlap
from ..models.workflow_trigger_spec_type import WorkflowTriggerSpecType, check_workflow_trigger_spec_type
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_trigger_spec_filter import WorkflowTriggerSpecFilter


T = TypeVar("T", bound="WorkflowTriggerSpec")


@_attrs_define
class WorkflowTriggerSpec:
    """Manual start, a five-field recurring schedule, or an internal event start. Event triggers use source/event_type
    patterns and a JSON content filter; matching runs receive the full CloudEvents envelope as input. Event recipients
    and workflow definitions are captured when an event is accepted. Scheduled starts skip missed minutes and default to
    skipping overlapping runs. Scheduling is active only on the live default deployment and requires the workflow
    runtime. New deployments arm schedules before their next eligible minute.

    """

    type_: WorkflowTriggerSpecType
    schedule: str | Unset = UNSET
    """Five-field cron expression, required for schedule triggers."""
    timezone: str | Unset = UNSET
    """IANA timezone for schedule triggers; defaults to UTC."""
    input_: Any | Unset = UNSET
    """Fixed JSON input for scheduled runs, bounded by the workflow run-input limit."""
    overlap: WorkflowTriggerSpecOverlap | Unset = UNSET
    """Skip a minute while any run of this workflow is active, or allow overlap subject to the app run quota.
    Defaults to skip."""
    enabled: bool | Unset = UNSET
    """Whether the automatic trigger is enabled; defaults to true. Already accepted events and existing runs
    continue after disabling."""
    source: str | Unset = UNSET
    """Required for event triggers; exact source or edge wildcard pattern."""
    event_type: str | Unset = UNSET
    """Required for event triggers; exact event type or edge wildcard pattern."""
    filter_: WorkflowTriggerSpecFilter | Unset = UNSET
    """Optional JSON predicate evaluated against the CloudEvents envelope. Event triggers reject schedule,
    timezone, input, and overlap options."""

    def to_dict(self) -> dict[str, Any]:
        type_: str = self.type_

        schedule = self.schedule

        timezone = self.timezone

        input_ = self.input_

        overlap: str | Unset = UNSET
        if not isinstance(self.overlap, Unset):
            overlap = self.overlap

        enabled = self.enabled

        source = self.source

        event_type = self.event_type

        filter_: dict[str, Any] | Unset = UNSET
        if not isinstance(self.filter_, Unset):
            filter_ = self.filter_.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "type": type_,
            }
        )
        if schedule is not UNSET:
            field_dict["schedule"] = schedule
        if timezone is not UNSET:
            field_dict["timezone"] = timezone
        if input_ is not UNSET:
            field_dict["input"] = input_
        if overlap is not UNSET:
            field_dict["overlap"] = overlap
        if enabled is not UNSET:
            field_dict["enabled"] = enabled
        if source is not UNSET:
            field_dict["source"] = source
        if event_type is not UNSET:
            field_dict["event_type"] = event_type
        if filter_ is not UNSET:
            field_dict["filter"] = filter_

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_trigger_spec_filter import WorkflowTriggerSpecFilter

        d = dict(src_dict)
        type_ = check_workflow_trigger_spec_type(d.pop("type"))

        schedule = d.pop("schedule", UNSET)

        timezone = d.pop("timezone", UNSET)

        input_ = d.pop("input", UNSET)

        _overlap = d.pop("overlap", UNSET)
        overlap: WorkflowTriggerSpecOverlap | Unset
        if isinstance(_overlap, Unset):
            overlap = UNSET
        else:
            overlap = check_workflow_trigger_spec_overlap(_overlap)

        enabled = d.pop("enabled", UNSET)

        source = d.pop("source", UNSET)

        event_type = d.pop("event_type", UNSET)

        _filter_ = d.pop("filter", UNSET)
        filter_: WorkflowTriggerSpecFilter | Unset
        if isinstance(_filter_, Unset):
            filter_ = UNSET
        else:
            filter_ = WorkflowTriggerSpecFilter.from_dict(_filter_)

        workflow_trigger_spec = cls(
            type_=type_,
            schedule=schedule,
            timezone=timezone,
            input_=input_,
            overlap=overlap,
            enabled=enabled,
            source=source,
            event_type=event_type,
            filter_=filter_,
        )

        return workflow_trigger_spec
