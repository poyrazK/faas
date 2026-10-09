from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_retention_sample import EventRetentionSample
    from ..models.event_storage_usage_response import EventStorageUsageResponse


T = TypeVar("T", bound="EventRetentionHealth")


@_attrs_define
class EventRetentionHealth:
    """Filtered receipt observations and unfiltered account-wide retained storage utilization."""

    observed_at: datetime.datetime
    window_seconds: int
    retained_receipts: int
    retained_bytes: int
    unknown_deadline_receipts: int
    """Settled receipts missing a settlement timestamp; expiry cannot be determined."""
    unsettled_receipts: int
    held_receipts: int
    running_backfill_holds: int
    retryable_backfill_holds: int
    held_due_receipts: int
    eligible_for_pruning: int
    expiring_receipts: int
    storage: EventStorageUsageResponse
    """Retained customer event counts, logical JSON bytes, pending age and current account budgets."""
    storage_count_utilization_pct: float
    storage_bytes_utilization_pct: float
    storage_utilization_pct: float
    sample: list[EventRetentionSample]
    sample_truncated: bool
    event_source: str | Unset = UNSET
    app_id: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        observed_at = self.observed_at.isoformat()

        window_seconds = self.window_seconds

        retained_receipts = self.retained_receipts

        retained_bytes = self.retained_bytes

        unknown_deadline_receipts = self.unknown_deadline_receipts

        unsettled_receipts = self.unsettled_receipts

        held_receipts = self.held_receipts

        running_backfill_holds = self.running_backfill_holds

        retryable_backfill_holds = self.retryable_backfill_holds

        held_due_receipts = self.held_due_receipts

        eligible_for_pruning = self.eligible_for_pruning

        expiring_receipts = self.expiring_receipts

        storage = self.storage.to_dict()

        storage_count_utilization_pct = self.storage_count_utilization_pct

        storage_bytes_utilization_pct = self.storage_bytes_utilization_pct

        storage_utilization_pct = self.storage_utilization_pct

        sample = []
        for sample_item_data in self.sample:
            sample_item = sample_item_data.to_dict()
            sample.append(sample_item)

        sample_truncated = self.sample_truncated

        event_source = self.event_source

        app_id: str | Unset = UNSET
        if not isinstance(self.app_id, Unset):
            app_id = str(self.app_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "observed_at": observed_at,
                "window_seconds": window_seconds,
                "retained_receipts": retained_receipts,
                "retained_bytes": retained_bytes,
                "unknown_deadline_receipts": unknown_deadline_receipts,
                "unsettled_receipts": unsettled_receipts,
                "held_receipts": held_receipts,
                "running_backfill_holds": running_backfill_holds,
                "retryable_backfill_holds": retryable_backfill_holds,
                "held_due_receipts": held_due_receipts,
                "eligible_for_pruning": eligible_for_pruning,
                "expiring_receipts": expiring_receipts,
                "storage": storage,
                "storage_count_utilization_pct": storage_count_utilization_pct,
                "storage_bytes_utilization_pct": storage_bytes_utilization_pct,
                "storage_utilization_pct": storage_utilization_pct,
                "sample": sample,
                "sample_truncated": sample_truncated,
            }
        )
        if event_source is not UNSET:
            field_dict["event_source"] = event_source
        if app_id is not UNSET:
            field_dict["app_id"] = app_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_retention_sample import EventRetentionSample
        from ..models.event_storage_usage_response import EventStorageUsageResponse

        d = dict(src_dict)
        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        window_seconds = d.pop("window_seconds")

        retained_receipts = d.pop("retained_receipts")

        retained_bytes = d.pop("retained_bytes")

        unknown_deadline_receipts = d.pop("unknown_deadline_receipts")

        unsettled_receipts = d.pop("unsettled_receipts")

        held_receipts = d.pop("held_receipts")

        running_backfill_holds = d.pop("running_backfill_holds")

        retryable_backfill_holds = d.pop("retryable_backfill_holds")

        held_due_receipts = d.pop("held_due_receipts")

        eligible_for_pruning = d.pop("eligible_for_pruning")

        expiring_receipts = d.pop("expiring_receipts")

        storage = EventStorageUsageResponse.from_dict(d.pop("storage"))

        storage_count_utilization_pct = d.pop("storage_count_utilization_pct")

        storage_bytes_utilization_pct = d.pop("storage_bytes_utilization_pct")

        storage_utilization_pct = d.pop("storage_utilization_pct")

        sample = []
        _sample = d.pop("sample")
        for sample_item_data in _sample:
            sample_item = EventRetentionSample.from_dict(sample_item_data)

            sample.append(sample_item)

        sample_truncated = d.pop("sample_truncated")

        event_source = d.pop("event_source", UNSET)

        _app_id = d.pop("app_id", UNSET)
        app_id: UUID | Unset
        if isinstance(_app_id, Unset):
            app_id = UNSET
        else:
            app_id = UUID(_app_id)

        event_retention_health = cls(
            observed_at=observed_at,
            window_seconds=window_seconds,
            retained_receipts=retained_receipts,
            retained_bytes=retained_bytes,
            unknown_deadline_receipts=unknown_deadline_receipts,
            unsettled_receipts=unsettled_receipts,
            held_receipts=held_receipts,
            running_backfill_holds=running_backfill_holds,
            retryable_backfill_holds=retryable_backfill_holds,
            held_due_receipts=held_due_receipts,
            eligible_for_pruning=eligible_for_pruning,
            expiring_receipts=expiring_receipts,
            storage=storage,
            storage_count_utilization_pct=storage_count_utilization_pct,
            storage_bytes_utilization_pct=storage_bytes_utilization_pct,
            storage_utilization_pct=storage_utilization_pct,
            sample=sample,
            sample_truncated=sample_truncated,
            event_source=event_source,
            app_id=app_id,
        )

        return event_retention_health
