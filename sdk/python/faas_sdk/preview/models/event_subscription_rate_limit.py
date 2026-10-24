from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="EventSubscriptionRateLimit")


@_attrs_define
class EventSubscriptionRateLimit:
    """Projected token-bucket capacity at observed_at. Read-only metadata reserves no tokens. Missing rate_limit on older
    responses means unknown.

    """

    enabled: bool
    revision: int
    interval_millis: int
    burst: int
    available_tokens: int
    rate_limited: bool
    observed_at: datetime.datetime
    next_available_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        revision = self.revision

        interval_millis = self.interval_millis

        burst = self.burst

        available_tokens = self.available_tokens

        rate_limited = self.rate_limited

        observed_at = self.observed_at.isoformat()

        next_available_at: str | Unset = UNSET
        if not isinstance(self.next_available_at, Unset):
            next_available_at = self.next_available_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "enabled": enabled,
                "revision": revision,
                "interval_millis": interval_millis,
                "burst": burst,
                "available_tokens": available_tokens,
                "rate_limited": rate_limited,
                "observed_at": observed_at,
            }
        )
        if next_available_at is not UNSET:
            field_dict["next_available_at"] = next_available_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        enabled = d.pop("enabled")

        revision = d.pop("revision")

        interval_millis = d.pop("interval_millis")

        burst = d.pop("burst")

        available_tokens = d.pop("available_tokens")

        rate_limited = d.pop("rate_limited")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        _next_available_at = d.pop("next_available_at", UNSET)
        next_available_at: datetime.datetime | Unset
        if isinstance(_next_available_at, Unset):
            next_available_at = UNSET
        else:
            next_available_at = datetime.datetime.fromisoformat(_next_available_at)

        event_subscription_rate_limit = cls(
            enabled=enabled,
            revision=revision,
            interval_millis=interval_millis,
            burst=burst,
            available_tokens=available_tokens,
            rate_limited=rate_limited,
            observed_at=observed_at,
            next_available_at=next_available_at,
        )

        return event_subscription_rate_limit
