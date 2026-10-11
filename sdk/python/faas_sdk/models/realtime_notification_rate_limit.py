from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.realtime_notification_rate_limit_window_seconds import (
    RealtimeNotificationRateLimitWindowSeconds,
    check_realtime_notification_rate_limit_window_seconds,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="RealtimeNotificationRateLimit")


@_attrs_define
class RealtimeNotificationRateLimit:
    """Shared provider-delivery quota for a principal across registered devices."""

    max_notifications: int
    window_seconds: RealtimeNotificationRateLimitWindowSeconds
    allow_urgent_bypass: bool | Unset = False
    """Allow urgent alerts to bypass only this quota."""

    def to_dict(self) -> dict[str, Any]:
        max_notifications = self.max_notifications

        window_seconds: int = self.window_seconds

        allow_urgent_bypass = self.allow_urgent_bypass

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "max_notifications": max_notifications,
                "window_seconds": window_seconds,
            }
        )
        if allow_urgent_bypass is not UNSET:
            field_dict["allow_urgent_bypass"] = allow_urgent_bypass

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        max_notifications = d.pop("max_notifications")

        window_seconds = check_realtime_notification_rate_limit_window_seconds(d.pop("window_seconds"))

        allow_urgent_bypass = d.pop("allow_urgent_bypass", UNSET)

        realtime_notification_rate_limit = cls(
            max_notifications=max_notifications,
            window_seconds=window_seconds,
            allow_urgent_bypass=allow_urgent_bypass,
        )

        return realtime_notification_rate_limit
