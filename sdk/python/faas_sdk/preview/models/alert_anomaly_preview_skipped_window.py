from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.alert_anomaly_preview_skipped_window_reason import (
    AlertAnomalyPreviewSkippedWindowReason,
    check_alert_anomaly_preview_skipped_window_reason,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="AlertAnomalyPreviewSkippedWindow")


@_attrs_define
class AlertAnomalyPreviewSkippedWindow:
    """A historical checkpoint the anomaly evaluator could not use."""

    evaluated_at: datetime.datetime
    reason: AlertAnomalyPreviewSkippedWindowReason
    eligible_requests: int | Unset = UNSET
    """Eligible request count when error-rate or p95 latency telemetry is covered. For p95 this counts successful
    requests only."""
    required_requests: int | Unset = UNSET
    """Minimum eligible request count for a check skipped for insufficient traffic."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        evaluated_at = self.evaluated_at.isoformat()

        reason: str = self.reason

        eligible_requests = self.eligible_requests

        required_requests = self.required_requests

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "evaluated_at": evaluated_at,
                "reason": reason,
            }
        )
        if eligible_requests is not UNSET:
            field_dict["eligible_requests"] = eligible_requests
        if required_requests is not UNSET:
            field_dict["required_requests"] = required_requests

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        reason = check_alert_anomaly_preview_skipped_window_reason(d.pop("reason"))

        eligible_requests = d.pop("eligible_requests", UNSET)

        required_requests = d.pop("required_requests", UNSET)

        alert_anomaly_preview_skipped_window = cls(
            evaluated_at=evaluated_at,
            reason=reason,
            eligible_requests=eligible_requests,
            required_requests=required_requests,
        )

        alert_anomaly_preview_skipped_window.additional_properties = d
        return alert_anomaly_preview_skipped_window

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
