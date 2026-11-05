from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.debug_running_cause_code import DebugRunningCauseCode, check_debug_running_cause_code
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.debug_running_flow_summary import DebugRunningFlowSummary
    from ..models.debug_running_request_attribution import DebugRunningRequestAttribution


T = TypeVar("T", bound="DebugRunningCause")


@_attrs_define
class DebugRunningCause:
    """One observed scheduler reason an application remained resident. The
    platform reports evidence available at the scheduler boundary; it
    does not infer a protocol or predict a cost saving.

    """

    code: DebugRunningCauseCode
    summary: str
    instance_count: int
    open_connections: int | Unset = UNSET
    tail_tasks: int | Unset = UNSET
    mode: str | Unset = UNSET
    workload_class: str | Unset = UNSET
    last_activity_at: datetime.datetime | Unset = UNSET
    idle_deadline: datetime.datetime | Unset = UNSET
    request: DebugRunningRequestAttribution | Unset = UNSET
    """The nearest retained request-telemetry representative linked to a
    request-activity cause. A representative may contain several
    collapsed requests; match_delta_ms and count make that limitation
    explicit.
    """
    flow_topology: list[DebugRunningFlowSummary] | Unset = UNSET
    """Bounded endpoint-level flow summaries observed for the cause."""
    flow_topology_degraded: bool | Unset = UNSET
    """True when optional flow detail was unavailable or stale."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        code: str = self.code

        summary = self.summary

        instance_count = self.instance_count

        open_connections = self.open_connections

        tail_tasks = self.tail_tasks

        mode = self.mode

        workload_class = self.workload_class

        last_activity_at: str | Unset = UNSET
        if not isinstance(self.last_activity_at, Unset):
            last_activity_at = self.last_activity_at.isoformat()

        idle_deadline: str | Unset = UNSET
        if not isinstance(self.idle_deadline, Unset):
            idle_deadline = self.idle_deadline.isoformat()

        request: dict[str, Any] | Unset = UNSET
        if not isinstance(self.request, Unset):
            request = self.request.to_dict()

        flow_topology: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.flow_topology, Unset):
            flow_topology = []
            for flow_topology_item_data in self.flow_topology:
                flow_topology_item = flow_topology_item_data.to_dict()
                flow_topology.append(flow_topology_item)

        flow_topology_degraded = self.flow_topology_degraded

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "code": code,
                "summary": summary,
                "instance_count": instance_count,
            }
        )
        if open_connections is not UNSET:
            field_dict["open_connections"] = open_connections
        if tail_tasks is not UNSET:
            field_dict["tail_tasks"] = tail_tasks
        if mode is not UNSET:
            field_dict["mode"] = mode
        if workload_class is not UNSET:
            field_dict["workload_class"] = workload_class
        if last_activity_at is not UNSET:
            field_dict["last_activity_at"] = last_activity_at
        if idle_deadline is not UNSET:
            field_dict["idle_deadline"] = idle_deadline
        if request is not UNSET:
            field_dict["request"] = request
        if flow_topology is not UNSET:
            field_dict["flow_topology"] = flow_topology
        if flow_topology_degraded is not UNSET:
            field_dict["flow_topology_degraded"] = flow_topology_degraded

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.debug_running_flow_summary import DebugRunningFlowSummary
        from ..models.debug_running_request_attribution import DebugRunningRequestAttribution

        d = dict(src_dict)
        code = check_debug_running_cause_code(d.pop("code"))

        summary = d.pop("summary")

        instance_count = d.pop("instance_count")

        open_connections = d.pop("open_connections", UNSET)

        tail_tasks = d.pop("tail_tasks", UNSET)

        mode = d.pop("mode", UNSET)

        workload_class = d.pop("workload_class", UNSET)

        _last_activity_at = d.pop("last_activity_at", UNSET)
        last_activity_at: datetime.datetime | Unset
        if isinstance(_last_activity_at, Unset):
            last_activity_at = UNSET
        else:
            last_activity_at = datetime.datetime.fromisoformat(_last_activity_at)

        _idle_deadline = d.pop("idle_deadline", UNSET)
        idle_deadline: datetime.datetime | Unset
        if isinstance(_idle_deadline, Unset):
            idle_deadline = UNSET
        else:
            idle_deadline = datetime.datetime.fromisoformat(_idle_deadline)

        _request = d.pop("request", UNSET)
        request: DebugRunningRequestAttribution | Unset
        if isinstance(_request, Unset):
            request = UNSET
        else:
            request = DebugRunningRequestAttribution.from_dict(_request)

        _flow_topology = d.pop("flow_topology", UNSET)
        flow_topology: list[DebugRunningFlowSummary] | Unset = UNSET
        if _flow_topology is not UNSET:
            flow_topology = []
            for flow_topology_item_data in _flow_topology:
                flow_topology_item = DebugRunningFlowSummary.from_dict(flow_topology_item_data)

                flow_topology.append(flow_topology_item)

        flow_topology_degraded = d.pop("flow_topology_degraded", UNSET)

        debug_running_cause = cls(
            code=code,
            summary=summary,
            instance_count=instance_count,
            open_connections=open_connections,
            tail_tasks=tail_tasks,
            mode=mode,
            workload_class=workload_class,
            last_activity_at=last_activity_at,
            idle_deadline=idle_deadline,
            request=request,
            flow_topology=flow_topology,
            flow_topology_degraded=flow_topology_degraded,
        )

        debug_running_cause.additional_properties = d
        return debug_running_cause

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
