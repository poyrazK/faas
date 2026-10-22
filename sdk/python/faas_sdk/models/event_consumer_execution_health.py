from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_consumer_execution_health_coverage import (
    EventConsumerExecutionHealthCoverage,
    check_event_consumer_execution_health_coverage,
)
from ..models.event_consumer_execution_health_history_status import (
    EventConsumerExecutionHealthHistoryStatus,
    check_event_consumer_execution_health_history_status,
)
from ..models.event_consumer_execution_health_window_coverage import (
    EventConsumerExecutionHealthWindowCoverage,
    check_event_consumer_execution_health_window_coverage,
)
from ..models.event_consumer_execution_health_window_history_status import (
    EventConsumerExecutionHealthWindowHistoryStatus,
    check_event_consumer_execution_health_window_history_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_execution_lineage_retry_job_health import EventExecutionLineageRetryJobHealth


T = TypeVar("T", bound="EventConsumerExecutionHealth")


@_attrs_define
class EventConsumerExecutionHealth:
    """Current retained execution states for admitted application event deliveries and retained handler replays. Attempts
    and successful completion latency use an immutable, per-subscription outcome ledger with 30-day retention. Window
    evidence survives source deletion and is independent of retained execution-state truncation. Counts are bounded
    observations, include backfills, and are not unique event counts. Version filtering and routing failures are
    excluded. History completeness is established only for fully covered windows of newly instrumented subscriptions;
    legacy baselines remain unknown and evidence loss is partial.

    """

    subscription_id: UUID
    app_id: UUID
    observed_at: datetime.datetime
    window_start: datetime.datetime
    coverage: EventConsumerExecutionHealthCoverage
    history_complete: bool
    """True only for an instrumented, fully covered window with intact evidence and no truncation or unknown
    outcomes."""
    truncated: bool
    """More than 1000 retained delivery roots or 5000 linked invocations existed."""
    retained_roots: int
    missing_roots: int
    executions: int
    queued: int
    running: int
    retrying: int
    succeeded: int
    failed: int
    expired: int
    dead_lettered: int
    cancelled: int
    superseded: int
    unknown: int
    successful_attempts: int
    failed_attempts: int
    unknown_attempts: int
    window_completions: int
    window_dead_letters: int
    handler_failure_pct: float
    dead_letter_rate_per_second: float
    completion_latency_p95_seconds: float
    lineage_retry_jobs: EventExecutionLineageRetryJobHealth | Unset = UNSET
    """Bounded retained pending and paused lineage retry jobs at observed_at. Independent of the execution window
    and handler outcome completeness. Earliest retention deadlines are observed first. When counts_complete is false
    counts are lower bounds and oldest/next timestamps describe only observed jobs. Stalled pending is an advisory
    inactivity warning; pause/resume activity can reset inactivity without changing last progress. Legacy evaluated
    jobs without a progress timestamp report unknown progress. No handler payloads or case selections are included.
   """
    window_coverage: EventConsumerExecutionHealthWindowCoverage | Unset = UNSET
    """Evidence scope for attempt rates and completion latency; current invocation states retain their separate
    bounded scope."""
    window_history_complete: bool | Unset = UNSET
    """True when the requested ledger window has a known baseline and no lost, expired or unknown outcomes."""
    window_history_status: EventConsumerExecutionHealthWindowHistoryStatus | Unset = UNSET
    window_history_reason: str | Unset = UNSET
    """Explanation of ledger window eligibility."""
    window_coverage_started_at: datetime.datetime | Unset = UNSET
    """Ledger collection start; windows beginning earlier cannot qualify."""
    window_coverage_lost_through_at: datetime.datetime | Unset = UNSET
    """Evidence-loss high-water mark for this subscription. Eligible windows must begin strictly after it."""
    window_evidence_retain_until: datetime.datetime | Unset = UNSET
    """Earliest expiry of retained evidence in this window; absent for an empty window."""
    window_evidence_retention_seconds: int | Unset = UNSET
    """Configured ledger retention independently of source receipt and result retention."""
    latest_successful_completion_at: datetime.datetime | Unset = UNSET
    """Latest valid successful completion among the bounded retained executions; absent when no such completion is
    retained. Not an all-time watermark and not restricted to the attempt window."""
    history_status: EventConsumerExecutionHealthHistoryStatus | Unset = UNSET
    history_reason: str | Unset = UNSET
    """Stable explanation of coverage eligibility or evidence loss."""
    coverage_started_at: datetime.datetime | Unset = UNSET
    """Durable collection start; absent when no coverage record exists."""
    coverage_lost_at: datetime.datetime | Unset = UNSET
    """First persisted evidence loss; loss is not automatically cleared."""

    def to_dict(self) -> dict[str, Any]:
        subscription_id = str(self.subscription_id)

        app_id = str(self.app_id)

        observed_at = self.observed_at.isoformat()

        window_start = self.window_start.isoformat()

        coverage: str = self.coverage

        history_complete = self.history_complete

        truncated = self.truncated

        retained_roots = self.retained_roots

        missing_roots = self.missing_roots

        executions = self.executions

        queued = self.queued

        running = self.running

        retrying = self.retrying

        succeeded = self.succeeded

        failed = self.failed

        expired = self.expired

        dead_lettered = self.dead_lettered

        cancelled = self.cancelled

        superseded = self.superseded

        unknown = self.unknown

        successful_attempts = self.successful_attempts

        failed_attempts = self.failed_attempts

        unknown_attempts = self.unknown_attempts

        window_completions = self.window_completions

        window_dead_letters = self.window_dead_letters

        handler_failure_pct = self.handler_failure_pct

        dead_letter_rate_per_second = self.dead_letter_rate_per_second

        completion_latency_p95_seconds = self.completion_latency_p95_seconds

        lineage_retry_jobs: dict[str, Any] | Unset = UNSET
        if not isinstance(self.lineage_retry_jobs, Unset):
            lineage_retry_jobs = self.lineage_retry_jobs.to_dict()

        window_coverage: str | Unset = UNSET
        if not isinstance(self.window_coverage, Unset):
            window_coverage = self.window_coverage

        window_history_complete = self.window_history_complete

        window_history_status: str | Unset = UNSET
        if not isinstance(self.window_history_status, Unset):
            window_history_status = self.window_history_status

        window_history_reason = self.window_history_reason

        window_coverage_started_at: str | Unset = UNSET
        if not isinstance(self.window_coverage_started_at, Unset):
            window_coverage_started_at = self.window_coverage_started_at.isoformat()

        window_coverage_lost_through_at: str | Unset = UNSET
        if not isinstance(self.window_coverage_lost_through_at, Unset):
            window_coverage_lost_through_at = self.window_coverage_lost_through_at.isoformat()

        window_evidence_retain_until: str | Unset = UNSET
        if not isinstance(self.window_evidence_retain_until, Unset):
            window_evidence_retain_until = self.window_evidence_retain_until.isoformat()

        window_evidence_retention_seconds = self.window_evidence_retention_seconds

        latest_successful_completion_at: str | Unset = UNSET
        if not isinstance(self.latest_successful_completion_at, Unset):
            latest_successful_completion_at = self.latest_successful_completion_at.isoformat()

        history_status: str | Unset = UNSET
        if not isinstance(self.history_status, Unset):
            history_status = self.history_status

        history_reason = self.history_reason

        coverage_started_at: str | Unset = UNSET
        if not isinstance(self.coverage_started_at, Unset):
            coverage_started_at = self.coverage_started_at.isoformat()

        coverage_lost_at: str | Unset = UNSET
        if not isinstance(self.coverage_lost_at, Unset):
            coverage_lost_at = self.coverage_lost_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subscription_id": subscription_id,
                "app_id": app_id,
                "observed_at": observed_at,
                "window_start": window_start,
                "coverage": coverage,
                "history_complete": history_complete,
                "truncated": truncated,
                "retained_roots": retained_roots,
                "missing_roots": missing_roots,
                "executions": executions,
                "queued": queued,
                "running": running,
                "retrying": retrying,
                "succeeded": succeeded,
                "failed": failed,
                "expired": expired,
                "dead_lettered": dead_lettered,
                "cancelled": cancelled,
                "superseded": superseded,
                "unknown": unknown,
                "successful_attempts": successful_attempts,
                "failed_attempts": failed_attempts,
                "unknown_attempts": unknown_attempts,
                "window_completions": window_completions,
                "window_dead_letters": window_dead_letters,
                "handler_failure_pct": handler_failure_pct,
                "dead_letter_rate_per_second": dead_letter_rate_per_second,
                "completion_latency_p95_seconds": completion_latency_p95_seconds,
            }
        )
        if lineage_retry_jobs is not UNSET:
            field_dict["lineage_retry_jobs"] = lineage_retry_jobs
        if window_coverage is not UNSET:
            field_dict["window_coverage"] = window_coverage
        if window_history_complete is not UNSET:
            field_dict["window_history_complete"] = window_history_complete
        if window_history_status is not UNSET:
            field_dict["window_history_status"] = window_history_status
        if window_history_reason is not UNSET:
            field_dict["window_history_reason"] = window_history_reason
        if window_coverage_started_at is not UNSET:
            field_dict["window_coverage_started_at"] = window_coverage_started_at
        if window_coverage_lost_through_at is not UNSET:
            field_dict["window_coverage_lost_through_at"] = window_coverage_lost_through_at
        if window_evidence_retain_until is not UNSET:
            field_dict["window_evidence_retain_until"] = window_evidence_retain_until
        if window_evidence_retention_seconds is not UNSET:
            field_dict["window_evidence_retention_seconds"] = window_evidence_retention_seconds
        if latest_successful_completion_at is not UNSET:
            field_dict["latest_successful_completion_at"] = latest_successful_completion_at
        if history_status is not UNSET:
            field_dict["history_status"] = history_status
        if history_reason is not UNSET:
            field_dict["history_reason"] = history_reason
        if coverage_started_at is not UNSET:
            field_dict["coverage_started_at"] = coverage_started_at
        if coverage_lost_at is not UNSET:
            field_dict["coverage_lost_at"] = coverage_lost_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_execution_lineage_retry_job_health import EventExecutionLineageRetryJobHealth

        d = dict(src_dict)
        subscription_id = UUID(d.pop("subscription_id"))

        app_id = UUID(d.pop("app_id"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        window_start = datetime.datetime.fromisoformat(d.pop("window_start"))

        coverage = check_event_consumer_execution_health_coverage(d.pop("coverage"))

        history_complete = d.pop("history_complete")

        truncated = d.pop("truncated")

        retained_roots = d.pop("retained_roots")

        missing_roots = d.pop("missing_roots")

        executions = d.pop("executions")

        queued = d.pop("queued")

        running = d.pop("running")

        retrying = d.pop("retrying")

        succeeded = d.pop("succeeded")

        failed = d.pop("failed")

        expired = d.pop("expired")

        dead_lettered = d.pop("dead_lettered")

        cancelled = d.pop("cancelled")

        superseded = d.pop("superseded")

        unknown = d.pop("unknown")

        successful_attempts = d.pop("successful_attempts")

        failed_attempts = d.pop("failed_attempts")

        unknown_attempts = d.pop("unknown_attempts")

        window_completions = d.pop("window_completions")

        window_dead_letters = d.pop("window_dead_letters")

        handler_failure_pct = d.pop("handler_failure_pct")

        dead_letter_rate_per_second = d.pop("dead_letter_rate_per_second")

        completion_latency_p95_seconds = d.pop("completion_latency_p95_seconds")

        _lineage_retry_jobs = d.pop("lineage_retry_jobs", UNSET)
        lineage_retry_jobs: EventExecutionLineageRetryJobHealth | Unset
        if isinstance(_lineage_retry_jobs, Unset):
            lineage_retry_jobs = UNSET
        else:
            lineage_retry_jobs = EventExecutionLineageRetryJobHealth.from_dict(_lineage_retry_jobs)

        _window_coverage = d.pop("window_coverage", UNSET)
        window_coverage: EventConsumerExecutionHealthWindowCoverage | Unset
        if isinstance(_window_coverage, Unset):
            window_coverage = UNSET
        else:
            window_coverage = check_event_consumer_execution_health_window_coverage(_window_coverage)

        window_history_complete = d.pop("window_history_complete", UNSET)

        _window_history_status = d.pop("window_history_status", UNSET)
        window_history_status: EventConsumerExecutionHealthWindowHistoryStatus | Unset
        if isinstance(_window_history_status, Unset):
            window_history_status = UNSET
        else:
            window_history_status = check_event_consumer_execution_health_window_history_status(_window_history_status)

        window_history_reason = d.pop("window_history_reason", UNSET)

        _window_coverage_started_at = d.pop("window_coverage_started_at", UNSET)
        window_coverage_started_at: datetime.datetime | Unset
        if isinstance(_window_coverage_started_at, Unset):
            window_coverage_started_at = UNSET
        else:
            window_coverage_started_at = datetime.datetime.fromisoformat(_window_coverage_started_at)

        _window_coverage_lost_through_at = d.pop("window_coverage_lost_through_at", UNSET)
        window_coverage_lost_through_at: datetime.datetime | Unset
        if isinstance(_window_coverage_lost_through_at, Unset):
            window_coverage_lost_through_at = UNSET
        else:
            window_coverage_lost_through_at = datetime.datetime.fromisoformat(_window_coverage_lost_through_at)

        _window_evidence_retain_until = d.pop("window_evidence_retain_until", UNSET)
        window_evidence_retain_until: datetime.datetime | Unset
        if isinstance(_window_evidence_retain_until, Unset):
            window_evidence_retain_until = UNSET
        else:
            window_evidence_retain_until = datetime.datetime.fromisoformat(_window_evidence_retain_until)

        window_evidence_retention_seconds = d.pop("window_evidence_retention_seconds", UNSET)

        _latest_successful_completion_at = d.pop("latest_successful_completion_at", UNSET)
        latest_successful_completion_at: datetime.datetime | Unset
        if isinstance(_latest_successful_completion_at, Unset):
            latest_successful_completion_at = UNSET
        else:
            latest_successful_completion_at = datetime.datetime.fromisoformat(_latest_successful_completion_at)

        _history_status = d.pop("history_status", UNSET)
        history_status: EventConsumerExecutionHealthHistoryStatus | Unset
        if isinstance(_history_status, Unset):
            history_status = UNSET
        else:
            history_status = check_event_consumer_execution_health_history_status(_history_status)

        history_reason = d.pop("history_reason", UNSET)

        _coverage_started_at = d.pop("coverage_started_at", UNSET)
        coverage_started_at: datetime.datetime | Unset
        if isinstance(_coverage_started_at, Unset):
            coverage_started_at = UNSET
        else:
            coverage_started_at = datetime.datetime.fromisoformat(_coverage_started_at)

        _coverage_lost_at = d.pop("coverage_lost_at", UNSET)
        coverage_lost_at: datetime.datetime | Unset
        if isinstance(_coverage_lost_at, Unset):
            coverage_lost_at = UNSET
        else:
            coverage_lost_at = datetime.datetime.fromisoformat(_coverage_lost_at)

        event_consumer_execution_health = cls(
            subscription_id=subscription_id,
            app_id=app_id,
            observed_at=observed_at,
            window_start=window_start,
            coverage=coverage,
            history_complete=history_complete,
            truncated=truncated,
            retained_roots=retained_roots,
            missing_roots=missing_roots,
            executions=executions,
            queued=queued,
            running=running,
            retrying=retrying,
            succeeded=succeeded,
            failed=failed,
            expired=expired,
            dead_lettered=dead_lettered,
            cancelled=cancelled,
            superseded=superseded,
            unknown=unknown,
            successful_attempts=successful_attempts,
            failed_attempts=failed_attempts,
            unknown_attempts=unknown_attempts,
            window_completions=window_completions,
            window_dead_letters=window_dead_letters,
            handler_failure_pct=handler_failure_pct,
            dead_letter_rate_per_second=dead_letter_rate_per_second,
            completion_latency_p95_seconds=completion_latency_p95_seconds,
            lineage_retry_jobs=lineage_retry_jobs,
            window_coverage=window_coverage,
            window_history_complete=window_history_complete,
            window_history_status=window_history_status,
            window_history_reason=window_history_reason,
            window_coverage_started_at=window_coverage_started_at,
            window_coverage_lost_through_at=window_coverage_lost_through_at,
            window_evidence_retain_until=window_evidence_retain_until,
            window_evidence_retention_seconds=window_evidence_retention_seconds,
            latest_successful_completion_at=latest_successful_completion_at,
            history_status=history_status,
            history_reason=history_reason,
            coverage_started_at=coverage_started_at,
            coverage_lost_at=coverage_lost_at,
        )

        return event_consumer_execution_health
