from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="AlertAnomalyPreviewBreach")


@_attrs_define
class AlertAnomalyPreviewBreach:
    """One preview checkpoint that met the anomaly comparison and minimum impact requirement."""

    evaluated_at: datetime.datetime
    observed: float
    """Observed request count, error rate in percent, or p95 latency in milliseconds."""
    baseline: float
    """Median metric value of qualifying historical windows at the same time of day."""
    trigger_value: float
    """Baseline-derived trigger or the absolute rate threshold for a zero-baseline fallback."""
    eligible_requests: int | Unset = UNSET
    """Eligible request count for an error-rate or p95 latency check. For p95 this counts successful requests only."""
    failed_requests: int | Unset = UNSET
    """Failed requests for a zero-baseline fallback breach."""
    zero_baseline_fallback: bool | Unset = UNSET
    """True when the absolute fallback threshold triggered this breach."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        evaluated_at = self.evaluated_at.isoformat()

        observed = self.observed

        baseline = self.baseline

        trigger_value = self.trigger_value

        eligible_requests = self.eligible_requests

        failed_requests = self.failed_requests

        zero_baseline_fallback = self.zero_baseline_fallback

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "evaluated_at": evaluated_at,
                "observed": observed,
                "baseline": baseline,
                "trigger_value": trigger_value,
            }
        )
        if eligible_requests is not UNSET:
            field_dict["eligible_requests"] = eligible_requests
        if failed_requests is not UNSET:
            field_dict["failed_requests"] = failed_requests
        if zero_baseline_fallback is not UNSET:
            field_dict["zero_baseline_fallback"] = zero_baseline_fallback

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        observed = d.pop("observed")

        baseline = d.pop("baseline")

        trigger_value = d.pop("trigger_value")

        eligible_requests = d.pop("eligible_requests", UNSET)

        failed_requests = d.pop("failed_requests", UNSET)

        zero_baseline_fallback = d.pop("zero_baseline_fallback", UNSET)

        alert_anomaly_preview_breach = cls(
            evaluated_at=evaluated_at,
            observed=observed,
            baseline=baseline,
            trigger_value=trigger_value,
            eligible_requests=eligible_requests,
            failed_requests=failed_requests,
            zero_baseline_fallback=zero_baseline_fallback,
        )

        alert_anomaly_preview_breach.additional_properties = d
        return alert_anomaly_preview_breach

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
