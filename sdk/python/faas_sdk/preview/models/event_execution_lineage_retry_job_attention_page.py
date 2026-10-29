from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.event_execution_lineage_retry_job_attention_page_coverage import (
    EventExecutionLineageRetryJobAttentionPageCoverage,
    check_event_execution_lineage_retry_job_attention_page_coverage,
)
from ..models.event_execution_lineage_retry_job_attention_page_filter import (
    EventExecutionLineageRetryJobAttentionPageFilter,
    check_event_execution_lineage_retry_job_attention_page_filter,
)

T = TypeVar("T", bound="EventExecutionLineageRetryJobAttentionPage")


@_attrs_define
class EventExecutionLineageRetryJobAttentionPage:
    """Current matching-page observations; no total count or frozen membership is promised."""

    coverage: EventExecutionLineageRetryJobAttentionPageCoverage
    filter_: EventExecutionLineageRetryJobAttentionPageFilter
    observed_at: datetime.datetime
    has_more: bool
    """Another matching row was observed after this page; consistent with next_after."""
    inactivity_warning_seconds: int
    retention_warning_seconds: int

    def to_dict(self) -> dict[str, Any]:
        coverage: str = self.coverage

        filter_: str = self.filter_

        observed_at = self.observed_at.isoformat()

        has_more = self.has_more

        inactivity_warning_seconds = self.inactivity_warning_seconds

        retention_warning_seconds = self.retention_warning_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "coverage": coverage,
                "filter": filter_,
                "observed_at": observed_at,
                "has_more": has_more,
                "inactivity_warning_seconds": inactivity_warning_seconds,
                "retention_warning_seconds": retention_warning_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        coverage = check_event_execution_lineage_retry_job_attention_page_coverage(d.pop("coverage"))

        filter_ = check_event_execution_lineage_retry_job_attention_page_filter(d.pop("filter"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        has_more = d.pop("has_more")

        inactivity_warning_seconds = d.pop("inactivity_warning_seconds")

        retention_warning_seconds = d.pop("retention_warning_seconds")

        event_execution_lineage_retry_job_attention_page = cls(
            coverage=coverage,
            filter_=filter_,
            observed_at=observed_at,
            has_more=has_more,
            inactivity_warning_seconds=inactivity_warning_seconds,
            retention_warning_seconds=retention_warning_seconds,
        )

        return event_execution_lineage_retry_job_attention_page
