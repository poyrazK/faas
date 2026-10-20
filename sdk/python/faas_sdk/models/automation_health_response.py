from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.automation_health_response_status_counts import AutomationHealthResponseStatusCounts
    from ..models.automation_health_run import AutomationHealthRun
    from ..models.automation_health_step_failure import AutomationHealthStepFailure
    from ..models.automation_queue_health import AutomationQueueHealth


T = TypeVar("T", bound="AutomationHealthResponse")


@_attrs_define
class AutomationHealthResponse:
    """Bounded aggregate automation health. Customer payloads and error strings are never returned."""

    app_slug: str
    automation_name: str
    window_start: datetime.datetime
    window_end: datetime.datetime
    run_count: int
    completed_run_count: int
    active_run_count: int
    """Current non-terminal runs that have started, including retries and parked waits; independent of the
    requested health window."""
    queued_run_count: int
    """Current pending runs that have not started; independent of the requested health window."""
    success_rate: float
    """Succeeded runs divided by succeeded, failed and dead runs; zero when none completed."""
    status_counts: AutomationHealthResponseStatusCounts
    failed_steps: list[AutomationHealthStepFailure]
    queue: AutomationQueueHealth | Unset = UNSET
    """Current app-owned queue diagnostics independent of the historical health window. Each waiting run has one
    primary reason; future wakes take precedence followed by app, tenant and workflow capacity. No payloads or
    tenant identities, global worker occupancy or completion estimates are returned."""
    p50_duration_ms: int | Unset = UNSET
    """Median duration of completed runs with start and finish timestamps; omitted when no samples exist."""
    p95_duration_ms: int | Unset = UNSET
    """95th percentile duration of completed runs with start and finish timestamps; omitted when no samples exist."""
    last_run: AutomationHealthRun | Unset = UNSET
    """Safe recent-run identity and timestamps; workflow input, output and error text are omitted."""
    last_success: AutomationHealthRun | Unset = UNSET
    """Safe recent-run identity and timestamps; workflow input, output and error text are omitted."""
    last_failure: AutomationHealthRun | Unset = UNSET
    """Safe recent-run identity and timestamps; workflow input, output and error text are omitted."""

    def to_dict(self) -> dict[str, Any]:
        app_slug = self.app_slug

        automation_name = self.automation_name

        window_start = self.window_start.isoformat()

        window_end = self.window_end.isoformat()

        run_count = self.run_count

        completed_run_count = self.completed_run_count

        active_run_count = self.active_run_count

        queued_run_count = self.queued_run_count

        success_rate = self.success_rate

        status_counts = self.status_counts.to_dict()

        failed_steps = []
        for failed_steps_item_data in self.failed_steps:
            failed_steps_item = failed_steps_item_data.to_dict()
            failed_steps.append(failed_steps_item)

        queue: dict[str, Any] | Unset = UNSET
        if not isinstance(self.queue, Unset):
            queue = self.queue.to_dict()

        p50_duration_ms = self.p50_duration_ms

        p95_duration_ms = self.p95_duration_ms

        last_run: dict[str, Any] | Unset = UNSET
        if not isinstance(self.last_run, Unset):
            last_run = self.last_run.to_dict()

        last_success: dict[str, Any] | Unset = UNSET
        if not isinstance(self.last_success, Unset):
            last_success = self.last_success.to_dict()

        last_failure: dict[str, Any] | Unset = UNSET
        if not isinstance(self.last_failure, Unset):
            last_failure = self.last_failure.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_slug": app_slug,
                "automation_name": automation_name,
                "window_start": window_start,
                "window_end": window_end,
                "run_count": run_count,
                "completed_run_count": completed_run_count,
                "active_run_count": active_run_count,
                "queued_run_count": queued_run_count,
                "success_rate": success_rate,
                "status_counts": status_counts,
                "failed_steps": failed_steps,
            }
        )
        if queue is not UNSET:
            field_dict["queue"] = queue
        if p50_duration_ms is not UNSET:
            field_dict["p50_duration_ms"] = p50_duration_ms
        if p95_duration_ms is not UNSET:
            field_dict["p95_duration_ms"] = p95_duration_ms
        if last_run is not UNSET:
            field_dict["last_run"] = last_run
        if last_success is not UNSET:
            field_dict["last_success"] = last_success
        if last_failure is not UNSET:
            field_dict["last_failure"] = last_failure

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_health_response_status_counts import AutomationHealthResponseStatusCounts
        from ..models.automation_health_run import AutomationHealthRun
        from ..models.automation_health_step_failure import AutomationHealthStepFailure
        from ..models.automation_queue_health import AutomationQueueHealth

        d = dict(src_dict)
        app_slug = d.pop("app_slug")

        automation_name = d.pop("automation_name")

        window_start = datetime.datetime.fromisoformat(d.pop("window_start"))

        window_end = datetime.datetime.fromisoformat(d.pop("window_end"))

        run_count = d.pop("run_count")

        completed_run_count = d.pop("completed_run_count")

        active_run_count = d.pop("active_run_count")

        queued_run_count = d.pop("queued_run_count")

        success_rate = d.pop("success_rate")

        status_counts = AutomationHealthResponseStatusCounts.from_dict(d.pop("status_counts"))

        failed_steps = []
        _failed_steps = d.pop("failed_steps")
        for failed_steps_item_data in _failed_steps:
            failed_steps_item = AutomationHealthStepFailure.from_dict(failed_steps_item_data)

            failed_steps.append(failed_steps_item)

        _queue = d.pop("queue", UNSET)
        queue: AutomationQueueHealth | Unset
        if isinstance(_queue, Unset):
            queue = UNSET
        else:
            queue = AutomationQueueHealth.from_dict(_queue)

        p50_duration_ms = d.pop("p50_duration_ms", UNSET)

        p95_duration_ms = d.pop("p95_duration_ms", UNSET)

        _last_run = d.pop("last_run", UNSET)
        last_run: AutomationHealthRun | Unset
        if isinstance(_last_run, Unset):
            last_run = UNSET
        else:
            last_run = AutomationHealthRun.from_dict(_last_run)

        _last_success = d.pop("last_success", UNSET)
        last_success: AutomationHealthRun | Unset
        if isinstance(_last_success, Unset):
            last_success = UNSET
        else:
            last_success = AutomationHealthRun.from_dict(_last_success)

        _last_failure = d.pop("last_failure", UNSET)
        last_failure: AutomationHealthRun | Unset
        if isinstance(_last_failure, Unset):
            last_failure = UNSET
        else:
            last_failure = AutomationHealthRun.from_dict(_last_failure)

        automation_health_response = cls(
            app_slug=app_slug,
            automation_name=automation_name,
            window_start=window_start,
            window_end=window_end,
            run_count=run_count,
            completed_run_count=completed_run_count,
            active_run_count=active_run_count,
            queued_run_count=queued_run_count,
            success_rate=success_rate,
            status_counts=status_counts,
            failed_steps=failed_steps,
            queue=queue,
            p50_duration_ms=p50_duration_ms,
            p95_duration_ms=p95_duration_ms,
            last_run=last_run,
            last_success=last_success,
            last_failure=last_failure,
        )

        return automation_health_response
