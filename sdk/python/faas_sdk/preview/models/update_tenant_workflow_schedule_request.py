from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.update_tenant_workflow_schedule_request_overlap import (
    UpdateTenantWorkflowScheduleRequestOverlap,
    check_update_tenant_workflow_schedule_request_overlap,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="UpdateTenantWorkflowScheduleRequest")


@_attrs_define
class UpdateTenantWorkflowScheduleRequest:
    """Tenant-owned schedule settings. Workflow input and workflow steps cannot be changed here."""

    expected_version: int
    """Zero creates the first override; otherwise use the latest version returned by list or update."""
    schedule: str
    """Cron expression evaluated with the platform's five-field grammar."""
    timezone: str | Unset = UNSET
    """IANA timezone; omitted uses the published timezone or UTC."""
    overlap: UpdateTenantWorkflowScheduleRequestOverlap | Unset = UNSET
    """Omitted uses the published overlap behavior or skip."""
    enabled: bool | Unset = True
    """Whether this tenant's schedule is active."""

    def to_dict(self) -> dict[str, Any]:
        expected_version = self.expected_version

        schedule = self.schedule

        timezone = self.timezone

        overlap: str | Unset = UNSET
        if not isinstance(self.overlap, Unset):
            overlap = self.overlap

        enabled = self.enabled

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_version": expected_version,
                "schedule": schedule,
            }
        )
        if timezone is not UNSET:
            field_dict["timezone"] = timezone
        if overlap is not UNSET:
            field_dict["overlap"] = overlap
        if enabled is not UNSET:
            field_dict["enabled"] = enabled

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_version = d.pop("expected_version")

        schedule = d.pop("schedule")

        timezone = d.pop("timezone", UNSET)

        _overlap = d.pop("overlap", UNSET)
        overlap: UpdateTenantWorkflowScheduleRequestOverlap | Unset
        if isinstance(_overlap, Unset):
            overlap = UNSET
        else:
            overlap = check_update_tenant_workflow_schedule_request_overlap(_overlap)

        enabled = d.pop("enabled", UNSET)

        update_tenant_workflow_schedule_request = cls(
            expected_version=expected_version,
            schedule=schedule,
            timezone=timezone,
            overlap=overlap,
            enabled=enabled,
        )

        return update_tenant_workflow_schedule_request
