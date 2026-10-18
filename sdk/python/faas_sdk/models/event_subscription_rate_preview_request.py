from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="EventSubscriptionRatePreviewRequest")


@_attrs_define
class EventSubscriptionRatePreviewRequest:
    """Proposed bucket settings for a preview only; this request has no command UUID or mutation guard."""

    enabled: bool
    interval_millis: int
    burst: int

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        interval_millis = self.interval_millis

        burst = self.burst

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "enabled": enabled,
                "interval_millis": interval_millis,
                "burst": burst,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        enabled = d.pop("enabled")

        interval_millis = d.pop("interval_millis")

        burst = d.pop("burst")

        event_subscription_rate_preview_request = cls(
            enabled=enabled,
            interval_millis=interval_millis,
            burst=burst,
        )

        return event_subscription_rate_preview_request
