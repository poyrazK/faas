from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="IssueCompoundAlertRule")


@_attrs_define
class IssueCompoundAlertRule:
    """Named AND rule requiring event frequency and verified customer impact in one shared window."""

    key: str
    enabled: bool
    environment: str
    """Compound-rule environment selector; leave empty to evaluate every issue environment."""
    minimum_events: int
    minimum_customers: int
    window_seconds: int
    cooldown_seconds: int

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        enabled = self.enabled

        environment = self.environment

        minimum_events = self.minimum_events

        minimum_customers = self.minimum_customers

        window_seconds = self.window_seconds

        cooldown_seconds = self.cooldown_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "key": key,
                "enabled": enabled,
                "environment": environment,
                "minimum_events": minimum_events,
                "minimum_customers": minimum_customers,
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

        minimum_customers = d.pop("minimum_customers")

        window_seconds = d.pop("window_seconds")

        cooldown_seconds = d.pop("cooldown_seconds")

        issue_compound_alert_rule = cls(
            key=key,
            enabled=enabled,
            environment=environment,
            minimum_events=minimum_events,
            minimum_customers=minimum_customers,
            window_seconds=window_seconds,
            cooldown_seconds=cooldown_seconds,
        )

        return issue_compound_alert_rule
