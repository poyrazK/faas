from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.profile_capture_kinds_item import ProfileCaptureKindsItem, check_profile_capture_kinds_item
from ..models.profile_capture_status import ProfileCaptureStatus, check_profile_capture_status
from ..models.profile_capture_trigger import ProfileCaptureTrigger, check_profile_capture_trigger
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.profile_capture_profile import ProfileCaptureProfile


T = TypeVar("T", bound="ProfileCapture")


@_attrs_define
class ProfileCapture:
    """An on-demand capture. processes counts instrumented processes that saw the capture; zero means the instance has no
    collector.

    """

    id: UUID
    app_id: UUID
    status: ProfileCaptureStatus
    kinds: list[ProfileCaptureKindsItem]
    duration_seconds: int
    processes: int
    profiles: list[ProfileCaptureProfile]
    created_at: datetime.datetime
    expires_at: datetime.datetime
    requested_instance_id: UUID | Unset = UNSET
    trigger: ProfileCaptureTrigger | Unset = UNSET
    """Who started the capture; memory_pressure captures are queued automatically when an instance runs low on
    memory, slow_request captures (stacks and CPU) when a request stays in flight for 10 seconds, cpu_saturation
    captures (CPU) when an instance uses 90% of its CPU limit for 30 seconds."""
    request_id: str | Unset = UNSET
    """The x-faas-request-id of the in-flight request that started a slow_request capture."""
    request_trace_id: str | Unset = UNSET
    """The trace id that request's telemetry is stored under."""
    trigger_detail: str | Unset = UNSET
    instance_id: UUID | Unset = UNSET
    deployment_id: UUID | Unset = UNSET
    dropped: int | Unset = UNSET
    """Profiles discarded by the reply bounds."""
    reason: str | Unset = UNSET
    partial: bool | Unset = UNSET
    """The capture ended early, normally because the workload was OOM-killed; heap profiles are the last
    checkpoints before the kill."""
    oom_killed_at: datetime.datetime | Unset = UNSET
    """Set when the captured instance was OOM-killed within 15 minutes of the capture."""
    oom_peak_mb: int | Unset = UNSET
    """Memory in use when the instance was OOM-killed."""
    oom_plan_mb: int | Unset = UNSET
    """The instance's plan memory limit."""
    completed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        status: str = self.status

        kinds = []
        for kinds_item_data in self.kinds:
            kinds_item: str = kinds_item_data
            kinds.append(kinds_item)

        duration_seconds = self.duration_seconds

        processes = self.processes

        profiles = []
        for profiles_item_data in self.profiles:
            profiles_item = profiles_item_data.to_dict()
            profiles.append(profiles_item)

        created_at = self.created_at.isoformat()

        expires_at = self.expires_at.isoformat()

        requested_instance_id: str | Unset = UNSET
        if not isinstance(self.requested_instance_id, Unset):
            requested_instance_id = str(self.requested_instance_id)

        trigger: str | Unset = UNSET
        if not isinstance(self.trigger, Unset):
            trigger = self.trigger

        request_id = self.request_id

        request_trace_id = self.request_trace_id

        trigger_detail = self.trigger_detail

        instance_id: str | Unset = UNSET
        if not isinstance(self.instance_id, Unset):
            instance_id = str(self.instance_id)

        deployment_id: str | Unset = UNSET
        if not isinstance(self.deployment_id, Unset):
            deployment_id = str(self.deployment_id)

        dropped = self.dropped

        reason = self.reason

        partial = self.partial

        oom_killed_at: str | Unset = UNSET
        if not isinstance(self.oom_killed_at, Unset):
            oom_killed_at = self.oom_killed_at.isoformat()

        oom_peak_mb = self.oom_peak_mb

        oom_plan_mb = self.oom_plan_mb

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "status": status,
                "kinds": kinds,
                "duration_seconds": duration_seconds,
                "processes": processes,
                "profiles": profiles,
                "created_at": created_at,
                "expires_at": expires_at,
            }
        )
        if requested_instance_id is not UNSET:
            field_dict["requested_instance_id"] = requested_instance_id
        if trigger is not UNSET:
            field_dict["trigger"] = trigger
        if request_id is not UNSET:
            field_dict["request_id"] = request_id
        if request_trace_id is not UNSET:
            field_dict["request_trace_id"] = request_trace_id
        if trigger_detail is not UNSET:
            field_dict["trigger_detail"] = trigger_detail
        if instance_id is not UNSET:
            field_dict["instance_id"] = instance_id
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if dropped is not UNSET:
            field_dict["dropped"] = dropped
        if reason is not UNSET:
            field_dict["reason"] = reason
        if partial is not UNSET:
            field_dict["partial"] = partial
        if oom_killed_at is not UNSET:
            field_dict["oom_killed_at"] = oom_killed_at
        if oom_peak_mb is not UNSET:
            field_dict["oom_peak_mb"] = oom_peak_mb
        if oom_plan_mb is not UNSET:
            field_dict["oom_plan_mb"] = oom_plan_mb
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_capture_profile import ProfileCaptureProfile

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        status = check_profile_capture_status(d.pop("status"))

        kinds = []
        _kinds = d.pop("kinds")
        for kinds_item_data in _kinds:
            kinds_item = check_profile_capture_kinds_item(kinds_item_data)

            kinds.append(kinds_item)

        duration_seconds = d.pop("duration_seconds")

        processes = d.pop("processes")

        profiles = []
        _profiles = d.pop("profiles")
        for profiles_item_data in _profiles:
            profiles_item = ProfileCaptureProfile.from_dict(profiles_item_data)

            profiles.append(profiles_item)

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        _requested_instance_id = d.pop("requested_instance_id", UNSET)
        requested_instance_id: UUID | Unset
        if isinstance(_requested_instance_id, Unset):
            requested_instance_id = UNSET
        else:
            requested_instance_id = UUID(_requested_instance_id)

        _trigger = d.pop("trigger", UNSET)
        trigger: ProfileCaptureTrigger | Unset
        if isinstance(_trigger, Unset):
            trigger = UNSET
        else:
            trigger = check_profile_capture_trigger(_trigger)

        request_id = d.pop("request_id", UNSET)

        request_trace_id = d.pop("request_trace_id", UNSET)

        trigger_detail = d.pop("trigger_detail", UNSET)

        _instance_id = d.pop("instance_id", UNSET)
        instance_id: UUID | Unset
        if isinstance(_instance_id, Unset):
            instance_id = UNSET
        else:
            instance_id = UUID(_instance_id)

        _deployment_id = d.pop("deployment_id", UNSET)
        deployment_id: UUID | Unset
        if isinstance(_deployment_id, Unset):
            deployment_id = UNSET
        else:
            deployment_id = UUID(_deployment_id)

        dropped = d.pop("dropped", UNSET)

        reason = d.pop("reason", UNSET)

        partial = d.pop("partial", UNSET)

        _oom_killed_at = d.pop("oom_killed_at", UNSET)
        oom_killed_at: datetime.datetime | Unset
        if isinstance(_oom_killed_at, Unset):
            oom_killed_at = UNSET
        else:
            oom_killed_at = datetime.datetime.fromisoformat(_oom_killed_at)

        oom_peak_mb = d.pop("oom_peak_mb", UNSET)

        oom_plan_mb = d.pop("oom_plan_mb", UNSET)

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        profile_capture = cls(
            id=id,
            app_id=app_id,
            status=status,
            kinds=kinds,
            duration_seconds=duration_seconds,
            processes=processes,
            profiles=profiles,
            created_at=created_at,
            expires_at=expires_at,
            requested_instance_id=requested_instance_id,
            trigger=trigger,
            request_id=request_id,
            request_trace_id=request_trace_id,
            trigger_detail=trigger_detail,
            instance_id=instance_id,
            deployment_id=deployment_id,
            dropped=dropped,
            reason=reason,
            partial=partial,
            oom_killed_at=oom_killed_at,
            oom_peak_mb=oom_peak_mb,
            oom_plan_mb=oom_plan_mb,
            completed_at=completed_at,
        )

        profile_capture.additional_properties = d
        return profile_capture

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
