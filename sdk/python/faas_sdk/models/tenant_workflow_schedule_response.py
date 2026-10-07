from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.tenant_workflow_schedule_response_overlap import (
    TenantWorkflowScheduleResponseOverlap,
    check_tenant_workflow_schedule_response_overlap,
)

T = TypeVar("T", bound="TenantWorkflowScheduleResponse")


@_attrs_define
class TenantWorkflowScheduleResponse:
    """A tenant's effective cadence for an opted-in published schedule workflow."""

    workflow_name: str
    deployment_id: UUID
    schedule: str
    """Five-field cron expression."""
    timezone: str
    """Effective IANA timezone."""
    overlap: TenantWorkflowScheduleResponseOverlap
    enabled: bool
    tenant_configurable: bool
    customized: bool
    """Whether this tenant has saved an override."""
    version: int
    """Zero uses the published defaults; use this value as expected_version when updating."""

    def to_dict(self) -> dict[str, Any]:
        workflow_name = self.workflow_name

        deployment_id = str(self.deployment_id)

        schedule = self.schedule

        timezone = self.timezone

        overlap: str = self.overlap

        enabled = self.enabled

        tenant_configurable = self.tenant_configurable

        customized = self.customized

        version = self.version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "workflow_name": workflow_name,
                "deployment_id": deployment_id,
                "schedule": schedule,
                "timezone": timezone,
                "overlap": overlap,
                "enabled": enabled,
                "tenant_configurable": tenant_configurable,
                "customized": customized,
                "version": version,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        workflow_name = d.pop("workflow_name")

        deployment_id = UUID(d.pop("deployment_id"))

        schedule = d.pop("schedule")

        timezone = d.pop("timezone")

        overlap = check_tenant_workflow_schedule_response_overlap(d.pop("overlap"))

        enabled = d.pop("enabled")

        tenant_configurable = d.pop("tenant_configurable")

        customized = d.pop("customized")

        version = d.pop("version")

        tenant_workflow_schedule_response = cls(
            workflow_name=workflow_name,
            deployment_id=deployment_id,
            schedule=schedule,
            timezone=timezone,
            overlap=overlap,
            enabled=enabled,
            tenant_configurable=tenant_configurable,
            customized=customized,
            version=version,
        )

        return tenant_workflow_schedule_response
