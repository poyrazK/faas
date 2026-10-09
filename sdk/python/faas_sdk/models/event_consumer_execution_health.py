from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_consumer_execution_health_coverage import (
    EventConsumerExecutionHealthCoverage,
    check_event_consumer_execution_health_coverage,
)

T = TypeVar("T", bound="EventConsumerExecutionHealth")


@_attrs_define
class EventConsumerExecutionHealth:
    """Current retained execution states for admitted application event deliveries and retained handler replays. Attempts
    and successful completion latency use the requested window. Counts are bounded observations, include backfills, and
    are not unique event counts. Version filtering and routing failures are excluded. Historical coverage is always
    incomplete.

    """

    subscription_id: UUID
    app_id: UUID
    observed_at: datetime.datetime
    window_start: datetime.datetime
    coverage: EventConsumerExecutionHealthCoverage
    history_complete: bool
    """Receipt, invocation, and attempt retention make this an incomplete historical observation."""
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

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
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
        )

        return event_consumer_execution_health
