from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.mcp_readiness_attention_action_action import (
    MCPReadinessAttentionActionAction,
    check_mcp_readiness_attention_action_action,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_readiness_reminder_policy import MCPReadinessReminderPolicy


T = TypeVar("T", bound="MCPReadinessAttentionAction")


@_attrs_define
class MCPReadinessAttentionAction:
    """Version-fenced attention decision. Acknowledge accepts optional expires_at; configure_reminders requires
    reminder_policy; release accepts neither.

    """

    request_id: UUID
    """Reuse this UUID and identical body when retrying the same decision."""
    expected_version: int
    """Current incident attention version; zero is valid."""
    action: MCPReadinessAttentionActionAction
    reason: str
    """Nonblank explanation limited to 500 UTF-8 bytes."""
    expires_at: datetime.datetime | Unset = UNSET
    """Optional absolute acknowledgement expiry within the next 30 days."""
    reminder_policy: MCPReadinessReminderPolicy | Unset = UNSET
    """Per-incident lifetime creation budget. Both zero disables; otherwise interval is 60–86400 seconds and budget
    is 1–10. Reconfiguration never resets the count."""

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        expected_version = self.expected_version

        action: str = self.action

        reason = self.reason

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        reminder_policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.reminder_policy, Unset):
            reminder_policy = self.reminder_policy.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "request_id": request_id,
                "expected_version": expected_version,
                "action": action,
                "reason": reason,
            }
        )
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at
        if reminder_policy is not UNSET:
            field_dict["reminder_policy"] = reminder_policy

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_readiness_reminder_policy import MCPReadinessReminderPolicy

        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        expected_version = d.pop("expected_version")

        action = check_mcp_readiness_attention_action_action(d.pop("action"))

        reason = d.pop("reason")

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        _reminder_policy = d.pop("reminder_policy", UNSET)
        reminder_policy: MCPReadinessReminderPolicy | Unset
        if isinstance(_reminder_policy, Unset):
            reminder_policy = UNSET
        else:
            reminder_policy = MCPReadinessReminderPolicy.from_dict(_reminder_policy)

        mcp_readiness_attention_action = cls(
            request_id=request_id,
            expected_version=expected_version,
            action=action,
            reason=reason,
            expires_at=expires_at,
            reminder_policy=reminder_policy,
        )

        return mcp_readiness_attention_action
