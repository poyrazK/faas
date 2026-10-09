from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="EventRecoveryExecutionSummary")


@_attrs_define
class EventRecoveryExecutionSummary:
    """Observations of admitted execution-mode items, preferring saved terminal results over live records. Legacy
    admissions without evidence remain unknown. Counts sum to tracked_count and are separate from job admission state.
    Omitted for routing recovery. Saved results share recovery job retention.

    """

    saved_results: int
    """Subset of tracked_count with saved confirmed terminal evidence. Not an additional state bucket."""
    observed_at: datetime.datetime
    tracked_count: int
    queued: int
    running: int
    retrying: int
    succeeded: int
    failed: int
    dead_lettered: int
    expired: int
    cancelled: int
    superseded: int
    unknown: int

    def to_dict(self) -> dict[str, Any]:
        saved_results = self.saved_results

        observed_at = self.observed_at.isoformat()

        tracked_count = self.tracked_count

        queued = self.queued

        running = self.running

        retrying = self.retrying

        succeeded = self.succeeded

        failed = self.failed

        dead_lettered = self.dead_lettered

        expired = self.expired

        cancelled = self.cancelled

        superseded = self.superseded

        unknown = self.unknown

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "saved_results": saved_results,
                "observed_at": observed_at,
                "tracked_count": tracked_count,
                "queued": queued,
                "running": running,
                "retrying": retrying,
                "succeeded": succeeded,
                "failed": failed,
                "dead_lettered": dead_lettered,
                "expired": expired,
                "cancelled": cancelled,
                "superseded": superseded,
                "unknown": unknown,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        saved_results = d.pop("saved_results")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        tracked_count = d.pop("tracked_count")

        queued = d.pop("queued")

        running = d.pop("running")

        retrying = d.pop("retrying")

        succeeded = d.pop("succeeded")

        failed = d.pop("failed")

        dead_lettered = d.pop("dead_lettered")

        expired = d.pop("expired")

        cancelled = d.pop("cancelled")

        superseded = d.pop("superseded")

        unknown = d.pop("unknown")

        event_recovery_execution_summary = cls(
            saved_results=saved_results,
            observed_at=observed_at,
            tracked_count=tracked_count,
            queued=queued,
            running=running,
            retrying=retrying,
            succeeded=succeeded,
            failed=failed,
            dead_lettered=dead_lettered,
            expired=expired,
            cancelled=cancelled,
            superseded=superseded,
            unknown=unknown,
        )

        return event_recovery_execution_summary
