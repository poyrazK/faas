from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.workflow_trigger_spec_catch_up import WorkflowTriggerSpecCatchUp, check_workflow_trigger_spec_catch_up
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
    and workflow definitions are captured when an event is accepted. Scheduled starts skip missed minutes by default;
    catch_up latest recovers at most one unconsumed fire inside a bounded window. Overlapping runs are skipped by
    default. Scheduling is active only on the live default deployment and requires the workflow runtime. New deployments
    and changed triggers arm before firing. An app owner may mark a schedule tenant_configurable to let each linked
    customer manage only its own cadence, timezone, overlap behavior, and enabled state; catch-up remains owner-
    controlled.

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
    catch_up: WorkflowTriggerSpecCatchUp | Unset = UNSET
    """Schedule-only recovery policy; defaults to skip. Latest coalesces missed fires into at most one run inside
    the recovery window; a current-minute fire takes precedence. Quota and overlap skips consume the interval."""
    catch_up_window: str | Unset = UNSET
    """Schedule-only duration used with catch_up latest, between 1m and 24h. Defaults to 1h; fires exactly at the
    age limit are included. Other policies reject this field."""
    enabled: bool | Unset = UNSET
    """Whether the automatic trigger is enabled; defaults to true. Already accepted events and existing runs
    continue after disabling."""
    tenant_configurable: bool | Unset = UNSET
    """For schedule triggers, allow each linked platform tenant to manage its own schedule, timezone, overlap
    behavior, and enabled state. Workflow input and definition remain app-owned."""
    source: str | Unset = UNSET
    """Required for event triggers; exact source or edge wildcard pattern."""
    event_type: str | Unset = UNSET
    """Required for event triggers; exact event type or edge wildcard pattern."""
    filter_: WorkflowTriggerSpecFilter | Unset = UNSET
    """Optional JSON predicate evaluated against the CloudEvents envelope. Event triggers reject schedule,
    timezone, input, overlap, and catch-up options."""

    def to_dict(self) -> dict[str, Any]:
        type_: str = self.type_

        schedule = self.schedule

        timezone = self.timezone

        input_ = self.input_

        overlap: str | Unset = UNSET
        if not isinstance(self.overlap, Unset):
            overlap = self.overlap

        catch_up: str | Unset = UNSET
        if not isinstance(self.catch_up, Unset):
            catch_up = self.catch_up

        catch_up_window = self.catch_up_window

        enabled = self.enabled

        tenant_configurable = self.tenant_configurable

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
        if catch_up is not UNSET:
            field_dict["catch_up"] = catch_up
        if catch_up_window is not UNSET:
            field_dict["catch_up_window"] = catch_up_window
        if enabled is not UNSET:
            field_dict["enabled"] = enabled
        if tenant_configurable is not UNSET:
            field_dict["tenant_configurable"] = tenant_configurable
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

        _catch_up = d.pop("catch_up", UNSET)
        catch_up: WorkflowTriggerSpecCatchUp | Unset
        if isinstance(_catch_up, Unset):
            catch_up = UNSET
        else:
            catch_up = check_workflow_trigger_spec_catch_up(_catch_up)

        catch_up_window = d.pop("catch_up_window", UNSET)

        enabled = d.pop("enabled", UNSET)

        tenant_configurable = d.pop("tenant_configurable", UNSET)

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
            catch_up=catch_up,
            catch_up_window=catch_up_window,
            enabled=enabled,
            tenant_configurable=tenant_configurable,
            source=source,
            event_type=event_type,
            filter_=filter_,
        )

        return workflow_trigger_spec
