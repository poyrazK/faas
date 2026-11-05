from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.automation_queue_health_reason_counts import AutomationQueueHealthReasonCounts


T = TypeVar("T", bound="AutomationQueueHealth")


@_attrs_define
class AutomationQueueHealth:
    """Current app-owned queue diagnostics independent of the historical health window. Each waiting run has one primary
    reason; future wakes take precedence followed by app, tenant and workflow capacity. No payloads or tenant
    identities, global worker occupancy or completion estimates are returned.

    """

    observed_at: datetime.datetime
    waiting_run_count: int
    """Current pending and parked runs plus expired running leases for this automation; reason counts sum to this
    value."""
    due_run_count: int
    """Due waiting runs including capacity blockers and lease recovery."""
    stale_run_count: int
    """Running automation runs whose dispatch lease has expired."""
    oldest_due_age_seconds: float
    """Age since the oldest due run became eligible, bounded by creation time; zero if none is due."""
    app_running_count: int
    """Live automation dispatch claims across all workflows and tenants in this app; excludes expired leases and
    native operation custody."""
    app_dispatch_limit: int
    tenant_dispatch_limit: int
    """Live claim limit per tenant within this app."""
    app_at_capacity: bool
    reason_counts: AutomationQueueHealthReasonCounts

    def to_dict(self) -> dict[str, Any]:
        observed_at = self.observed_at.isoformat()

        waiting_run_count = self.waiting_run_count

        due_run_count = self.due_run_count

        stale_run_count = self.stale_run_count

        oldest_due_age_seconds = self.oldest_due_age_seconds

        app_running_count = self.app_running_count

        app_dispatch_limit = self.app_dispatch_limit

        tenant_dispatch_limit = self.tenant_dispatch_limit

        app_at_capacity = self.app_at_capacity

        reason_counts = self.reason_counts.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "observed_at": observed_at,
                "waiting_run_count": waiting_run_count,
                "due_run_count": due_run_count,
                "stale_run_count": stale_run_count,
                "oldest_due_age_seconds": oldest_due_age_seconds,
                "app_running_count": app_running_count,
                "app_dispatch_limit": app_dispatch_limit,
                "tenant_dispatch_limit": tenant_dispatch_limit,
                "app_at_capacity": app_at_capacity,
                "reason_counts": reason_counts,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_queue_health_reason_counts import AutomationQueueHealthReasonCounts

        d = dict(src_dict)
        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        waiting_run_count = d.pop("waiting_run_count")

        due_run_count = d.pop("due_run_count")

        stale_run_count = d.pop("stale_run_count")

        oldest_due_age_seconds = d.pop("oldest_due_age_seconds")

        app_running_count = d.pop("app_running_count")

        app_dispatch_limit = d.pop("app_dispatch_limit")

        tenant_dispatch_limit = d.pop("tenant_dispatch_limit")

        app_at_capacity = d.pop("app_at_capacity")

        reason_counts = AutomationQueueHealthReasonCounts.from_dict(d.pop("reason_counts"))

        automation_queue_health = cls(
            observed_at=observed_at,
            waiting_run_count=waiting_run_count,
            due_run_count=due_run_count,
            stale_run_count=stale_run_count,
            oldest_due_age_seconds=oldest_due_age_seconds,
            app_running_count=app_running_count,
            app_dispatch_limit=app_dispatch_limit,
            tenant_dispatch_limit=tenant_dispatch_limit,
            app_at_capacity=app_at_capacity,
            reason_counts=reason_counts,
        )

        return automation_queue_health
