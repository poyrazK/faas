from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="EventSubscriptionRateRequest")


@_attrs_define
class EventSubscriptionRateRequest:
    """Explicit guarded command for per-subscription token-bucket pacing. Disabled retains configuration without enforcing
    the bucket.

    """

    command_id: UUID
    """Nonzero command UUID; retained for 30 days, at most 128 per subscription."""
    expected_revision: int
    """Current bucket revision, initially zero. Mismatch records a rejected receipt."""
    enabled: bool
    interval_millis: int
    burst: int

    def to_dict(self) -> dict[str, Any]:
        command_id = str(self.command_id)

        expected_revision = self.expected_revision

        enabled = self.enabled

        interval_millis = self.interval_millis

        burst = self.burst

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "command_id": command_id,
                "expected_revision": expected_revision,
                "enabled": enabled,
                "interval_millis": interval_millis,
                "burst": burst,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        command_id = UUID(d.pop("command_id"))

        expected_revision = d.pop("expected_revision")

        enabled = d.pop("enabled")

        interval_millis = d.pop("interval_millis")

        burst = d.pop("burst")

        event_subscription_rate_request = cls(
            command_id=command_id,
            expected_revision=expected_revision,
            enabled=enabled,
            interval_millis=interval_millis,
            burst=burst,
        )

        return event_subscription_rate_request
