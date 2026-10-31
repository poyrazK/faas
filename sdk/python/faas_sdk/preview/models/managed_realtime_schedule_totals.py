from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ManagedRealtimeScheduleTotals")


@_attrs_define
class ManagedRealtimeScheduleTotals:
    """Totals over returned retained records, not lifetime analytics. Completed occurrences include successful one-time
    schedules and recurring completion counts.

    """

    pending: int
    paused: int
    published: int
    failed: int
    skipped: int
    canceled: int
    completed_occurrences: int
    skipped_occurrences: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        pending = self.pending

        paused = self.paused

        published = self.published

        failed = self.failed

        skipped = self.skipped

        canceled = self.canceled

        completed_occurrences = self.completed_occurrences

        skipped_occurrences = self.skipped_occurrences

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "pending": pending,
                "paused": paused,
                "published": published,
                "failed": failed,
                "skipped": skipped,
                "canceled": canceled,
                "completed_occurrences": completed_occurrences,
                "skipped_occurrences": skipped_occurrences,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        pending = d.pop("pending")

        paused = d.pop("paused")

        published = d.pop("published")

        failed = d.pop("failed")

        skipped = d.pop("skipped")

        canceled = d.pop("canceled")

        completed_occurrences = d.pop("completed_occurrences")

        skipped_occurrences = d.pop("skipped_occurrences")

        managed_realtime_schedule_totals = cls(
            pending=pending,
            paused=paused,
            published=published,
            failed=failed,
            skipped=skipped,
            canceled=canceled,
            completed_occurrences=completed_occurrences,
            skipped_occurrences=skipped_occurrences,
        )

        managed_realtime_schedule_totals.additional_properties = d
        return managed_realtime_schedule_totals

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
