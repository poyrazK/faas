from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.mcp_readiness_notification_action_action import (
    MCPReadinessNotificationActionAction,
    check_mcp_readiness_notification_action_action,
)
from ..models.mcp_readiness_notification_action_destination import (
    MCPReadinessNotificationActionDestination,
    check_mcp_readiness_notification_action_destination,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="MCPReadinessNotificationAction")


@_attrs_define
class MCPReadinessNotificationAction:
    """Version-fenced decision against a receiver head or the oldest event using an app-scoped idempotency key."""

    request_id: UUID
    """Reuse this UUID and the identical request body when retrying the same decision."""
    event_id: UUID
    action: MCPReadinessNotificationActionAction
    expected_version: int
    """Current head delivery_version from queue inspection."""
    reason: str
    """Nonblank operator explanation limited to 500 UTF-8 bytes."""
    destination: MCPReadinessNotificationActionDestination | Unset = UNSET
    """Selected receiver queue; omitted for an event-wide decision."""

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        event_id = str(self.event_id)

        action: str = self.action

        expected_version = self.expected_version

        reason = self.reason

        destination: str | Unset = UNSET
        if not isinstance(self.destination, Unset):
            destination = self.destination

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "request_id": request_id,
                "event_id": event_id,
                "action": action,
                "expected_version": expected_version,
                "reason": reason,
            }
        )
        if destination is not UNSET:
            field_dict["destination"] = destination

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        event_id = UUID(d.pop("event_id"))

        action = check_mcp_readiness_notification_action_action(d.pop("action"))

        expected_version = d.pop("expected_version")

        reason = d.pop("reason")

        _destination = d.pop("destination", UNSET)
        destination: MCPReadinessNotificationActionDestination | Unset
        if isinstance(_destination, Unset):
            destination = UNSET
        else:
            destination = check_mcp_readiness_notification_action_destination(_destination)

        mcp_readiness_notification_action = cls(
            request_id=request_id,
            event_id=event_id,
            action=action,
            expected_version=expected_version,
            reason=reason,
            destination=destination,
        )

        return mcp_readiness_notification_action
