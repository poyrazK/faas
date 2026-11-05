from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="AutomationQueueHealthReasonCounts")


@_attrs_define
class AutomationQueueHealthReasonCounts:
    ready: int
    """Due and passes observed run admission checks; does not guarantee immediate handler execution."""
    scheduled: int
    """Future scheduling deadline that is not a pending step retry."""
    retry_backoff: int
    """A pending step retry deadline equals the future persisted run wake."""
    parked_wait: int
    """Intentional future wait or timeout; inspect steps for timer, condition, event or callback details."""
    app_capacity: int
    tenant_capacity: int
    workflow_capacity: int
    """The captured workflow definition run budget is full."""

    def to_dict(self) -> dict[str, Any]:
        ready = self.ready

        scheduled = self.scheduled

        retry_backoff = self.retry_backoff

        parked_wait = self.parked_wait

        app_capacity = self.app_capacity

        tenant_capacity = self.tenant_capacity

        workflow_capacity = self.workflow_capacity

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "ready": ready,
                "scheduled": scheduled,
                "retry_backoff": retry_backoff,
                "parked_wait": parked_wait,
                "app_capacity": app_capacity,
                "tenant_capacity": tenant_capacity,
                "workflow_capacity": workflow_capacity,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        ready = d.pop("ready")

        scheduled = d.pop("scheduled")

        retry_backoff = d.pop("retry_backoff")

        parked_wait = d.pop("parked_wait")

        app_capacity = d.pop("app_capacity")

        tenant_capacity = d.pop("tenant_capacity")

        workflow_capacity = d.pop("workflow_capacity")

        automation_queue_health_reason_counts = cls(
            ready=ready,
            scheduled=scheduled,
            retry_backoff=retry_backoff,
            parked_wait=parked_wait,
            app_capacity=app_capacity,
            tenant_capacity=tenant_capacity,
            workflow_capacity=workflow_capacity,
        )

        return automation_queue_health_reason_counts
