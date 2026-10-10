from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="EventRecoveryNotificationHealthCounts")


@_attrs_define
class EventRecoveryNotificationHealthCounts:
    """Per-phase notification job counts. Incomplete evidence or candidate truncation makes these lower bounds; partial
    observations cannot clear alerts and can only trigger satisfied greater-than comparisons.

    """

    counts_complete: bool
    overdue_jobs: int
    dead_jobs: int
    unknown_jobs: int
    no_receivers_jobs: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        counts_complete = self.counts_complete

        overdue_jobs = self.overdue_jobs

        dead_jobs = self.dead_jobs

        unknown_jobs = self.unknown_jobs

        no_receivers_jobs = self.no_receivers_jobs

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "counts_complete": counts_complete,
                "overdue_jobs": overdue_jobs,
                "dead_jobs": dead_jobs,
                "unknown_jobs": unknown_jobs,
                "no_receivers_jobs": no_receivers_jobs,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        counts_complete = d.pop("counts_complete")

        overdue_jobs = d.pop("overdue_jobs")

        dead_jobs = d.pop("dead_jobs")

        unknown_jobs = d.pop("unknown_jobs")

        no_receivers_jobs = d.pop("no_receivers_jobs")

        event_recovery_notification_health_counts = cls(
            counts_complete=counts_complete,
            overdue_jobs=overdue_jobs,
            dead_jobs=dead_jobs,
            unknown_jobs=unknown_jobs,
            no_receivers_jobs=no_receivers_jobs,
        )

        event_recovery_notification_health_counts.additional_properties = d
        return event_recovery_notification_health_counts

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
