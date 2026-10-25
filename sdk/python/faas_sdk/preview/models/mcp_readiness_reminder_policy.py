from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="MCPReadinessReminderPolicy")


@_attrs_define
class MCPReadinessReminderPolicy:
    """Per-incident lifetime creation budget. Both zero disables; otherwise interval is 60–86400 seconds and budget is
    1–10. Reconfiguration never resets the count.

    """

    interval_seconds: int
    max_reminders: int

    def to_dict(self) -> dict[str, Any]:
        interval_seconds = self.interval_seconds

        max_reminders = self.max_reminders

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "interval_seconds": interval_seconds,
                "max_reminders": max_reminders,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        interval_seconds = d.pop("interval_seconds")

        max_reminders = d.pop("max_reminders")

        mcp_readiness_reminder_policy = cls(
            interval_seconds=interval_seconds,
            max_reminders=max_reminders,
        )

        return mcp_readiness_reminder_policy
