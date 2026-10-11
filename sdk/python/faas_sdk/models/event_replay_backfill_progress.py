from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="EventReplayBackfillProgress")


@_attrs_define
class EventReplayBackfillProgress:
    """Durable scan outcomes and current routing state for a backfill job."""

    scanned: int
    matched: int
    filtered: int
    pending: int
    processing: int
    enqueued: int
    """Routing admitted an invocation; handler completion is separate."""
    failed: int
    retryable_failed: int
    """Failed recipient deliveries eligible for the retry-failed operation."""
    skipped_captured: int
    skipped_unknown: int
    skipped_existing: int
    skipped_unsettled: int

    def to_dict(self) -> dict[str, Any]:
        scanned = self.scanned

        matched = self.matched

        filtered = self.filtered

        pending = self.pending

        processing = self.processing

        enqueued = self.enqueued

        failed = self.failed

        retryable_failed = self.retryable_failed

        skipped_captured = self.skipped_captured

        skipped_unknown = self.skipped_unknown

        skipped_existing = self.skipped_existing

        skipped_unsettled = self.skipped_unsettled

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "scanned": scanned,
                "matched": matched,
                "filtered": filtered,
                "pending": pending,
                "processing": processing,
                "enqueued": enqueued,
                "failed": failed,
                "retryable_failed": retryable_failed,
                "skipped_captured": skipped_captured,
                "skipped_unknown": skipped_unknown,
                "skipped_existing": skipped_existing,
                "skipped_unsettled": skipped_unsettled,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        scanned = d.pop("scanned")

        matched = d.pop("matched")

        filtered = d.pop("filtered")

        pending = d.pop("pending")

        processing = d.pop("processing")

        enqueued = d.pop("enqueued")

        failed = d.pop("failed")

        retryable_failed = d.pop("retryable_failed")

        skipped_captured = d.pop("skipped_captured")

        skipped_unknown = d.pop("skipped_unknown")

        skipped_existing = d.pop("skipped_existing")

        skipped_unsettled = d.pop("skipped_unsettled")

        event_replay_backfill_progress = cls(
            scanned=scanned,
            matched=matched,
            filtered=filtered,
            pending=pending,
            processing=processing,
            enqueued=enqueued,
            failed=failed,
            retryable_failed=retryable_failed,
            skipped_captured=skipped_captured,
            skipped_unknown=skipped_unknown,
            skipped_existing=skipped_existing,
            skipped_unsettled=skipped_unsettled,
        )

        return event_replay_backfill_progress
