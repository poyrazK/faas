from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowRecoveryReportTotals")


@_attrs_define
class WorkflowRecoveryReportTotals:
    """Observed event-window counts and matched retry timing, without causal or live-state inference."""

    attempts: int
    succeeded: int
    failed: int
    cancelled: int
    other_results: int
    without_observed_result: int
    """Attempts without a result observed in this window; not a live unresolved-child count."""
    pauses: int
    timed_results: int
    mean_retry_result_seconds: float | Unset = UNSET
    """Mean application-to-recorded-result time for matched child IDs with ordered timestamps; absent without
    timing samples."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        attempts = self.attempts

        succeeded = self.succeeded

        failed = self.failed

        cancelled = self.cancelled

        other_results = self.other_results

        without_observed_result = self.without_observed_result

        pauses = self.pauses

        timed_results = self.timed_results

        mean_retry_result_seconds = self.mean_retry_result_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "attempts": attempts,
                "succeeded": succeeded,
                "failed": failed,
                "cancelled": cancelled,
                "other_results": other_results,
                "without_observed_result": without_observed_result,
                "pauses": pauses,
                "timed_results": timed_results,
            }
        )
        if mean_retry_result_seconds is not UNSET:
            field_dict["mean_retry_result_seconds"] = mean_retry_result_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        attempts = d.pop("attempts")

        succeeded = d.pop("succeeded")

        failed = d.pop("failed")

        cancelled = d.pop("cancelled")

        other_results = d.pop("other_results")

        without_observed_result = d.pop("without_observed_result")

        pauses = d.pop("pauses")

        timed_results = d.pop("timed_results")

        mean_retry_result_seconds = d.pop("mean_retry_result_seconds", UNSET)

        workflow_recovery_report_totals = cls(
            attempts=attempts,
            succeeded=succeeded,
            failed=failed,
            cancelled=cancelled,
            other_results=other_results,
            without_observed_result=without_observed_result,
            pauses=pauses,
            timed_results=timed_results,
            mean_retry_result_seconds=mean_retry_result_seconds,
        )

        workflow_recovery_report_totals.additional_properties = d
        return workflow_recovery_report_totals

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
