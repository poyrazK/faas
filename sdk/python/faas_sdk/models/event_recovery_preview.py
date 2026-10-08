from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.event_recovery_preview_coverage import EventRecoveryPreviewCoverage, check_event_recovery_preview_coverage

if TYPE_CHECKING:
    from ..models.event_recovery_item import EventRecoveryItem


T = TypeVar("T", bound="EventRecoveryPreview")


@_attrs_define
class EventRecoveryPreview:
    observed_at: datetime.datetime
    coverage: EventRecoveryPreviewCoverage
    matched_count: int
    """Exact up to the job limit; a lower bound when exceeds_job_limit is true."""
    exceeds_job_limit: bool
    sample: list[EventRecoveryItem]

    def to_dict(self) -> dict[str, Any]:
        observed_at = self.observed_at.isoformat()

        coverage: str = self.coverage

        matched_count = self.matched_count

        exceeds_job_limit = self.exceeds_job_limit

        sample = []
        for sample_item_data in self.sample:
            sample_item = sample_item_data.to_dict()
            sample.append(sample_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "observed_at": observed_at,
                "coverage": coverage,
                "matched_count": matched_count,
                "exceeds_job_limit": exceeds_job_limit,
                "sample": sample,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_recovery_item import EventRecoveryItem

        d = dict(src_dict)
        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        coverage = check_event_recovery_preview_coverage(d.pop("coverage"))

        matched_count = d.pop("matched_count")

        exceeds_job_limit = d.pop("exceeds_job_limit")

        sample = []
        _sample = d.pop("sample")
        for sample_item_data in _sample:
            sample_item = EventRecoveryItem.from_dict(sample_item_data)

            sample.append(sample_item)

        event_recovery_preview = cls(
            observed_at=observed_at,
            coverage=coverage,
            matched_count=matched_count,
            exceeds_job_limit=exceeds_job_limit,
            sample=sample,
        )

        return event_recovery_preview
