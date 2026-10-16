from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="IssueFrequencyAlertRule")


@_attrs_define
class IssueFrequencyAlertRule:
    """Named event-frequency threshold with a bounded observation window, cooldown and optional filters."""

    key: str
    enabled: bool
    environment: str
    """Exact issue environment. Empty string matches all environments."""
    minimum_events: int
    window_seconds: int
    cooldown_seconds: int

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        enabled = self.enabled

        environment = self.environment

        minimum_events = self.minimum_events

        window_seconds = self.window_seconds

        cooldown_seconds = self.cooldown_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "key": key,
                "enabled": enabled,
                "environment": environment,
                "minimum_events": minimum_events,
                "window_seconds": window_seconds,
                "cooldown_seconds": cooldown_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        key = d.pop("key")

        enabled = d.pop("enabled")

        environment = d.pop("environment")

        minimum_events = d.pop("minimum_events")

        window_seconds = d.pop("window_seconds")

        cooldown_seconds = d.pop("cooldown_seconds")

        issue_frequency_alert_rule = cls(
            key=key,
            enabled=enabled,
            environment=environment,
            minimum_events=minimum_events,
            window_seconds=window_seconds,
            cooldown_seconds=cooldown_seconds,
        )

        return issue_frequency_alert_rule
