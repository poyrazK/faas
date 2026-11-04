from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.mcp_readiness_reminder_policy import MCPReadinessReminderPolicy


T = TypeVar("T", bound="SetMCPReadinessReminderDefaultsRequest")


@_attrs_define
class SetMCPReadinessReminderDefaultsRequest:
    """Complete replacement of rule reminder defaults with optimistic concurrency control."""

    expected_version: int
    """Current rule defaults version; required even when it is zero."""
    reminder_policy: MCPReadinessReminderPolicy
    """Per-incident lifetime creation budget. Both zero disables; otherwise interval is 60–86400 seconds and budget
    is 1–10. Reconfiguration never resets the count."""

    def to_dict(self) -> dict[str, Any]:
        expected_version = self.expected_version

        reminder_policy = self.reminder_policy.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_version": expected_version,
                "reminder_policy": reminder_policy,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_readiness_reminder_policy import MCPReadinessReminderPolicy

        d = dict(src_dict)
        expected_version = d.pop("expected_version")

        reminder_policy = MCPReadinessReminderPolicy.from_dict(d.pop("reminder_policy"))

        set_mcp_readiness_reminder_defaults_request = cls(
            expected_version=expected_version,
            reminder_policy=reminder_policy,
        )

        return set_mcp_readiness_reminder_defaults_request
