from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_readiness_notification_receipt_action import (
    MCPReadinessNotificationReceiptAction,
    check_mcp_readiness_notification_receipt_action,
)
from ..models.mcp_readiness_notification_receipt_destination import (
    MCPReadinessNotificationReceiptDestination,
    check_mcp_readiness_notification_receipt_destination,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="MCPReadinessNotificationReceipt")


@_attrs_define
class MCPReadinessNotificationReceipt:
    """Immutable recovery decision retained transactionally with the queue change."""

    request_id: UUID
    event_id: UUID
    action: MCPReadinessNotificationReceiptAction
    reason: str
    created_at: datetime.datetime
    created_by: UUID
    delivery_version: int
    destination: MCPReadinessNotificationReceiptDestination | Unset = UNSET
    """Receiver selected by the retained decision; absent for a whole-event receipt."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        event_id = str(self.event_id)

        action: str = self.action

        reason = self.reason

        created_at = self.created_at.isoformat()

        created_by = str(self.created_by)

        delivery_version = self.delivery_version

        destination: str | Unset = UNSET
        if not isinstance(self.destination, Unset):
            destination = self.destination

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "request_id": request_id,
                "event_id": event_id,
                "action": action,
                "reason": reason,
                "created_at": created_at,
                "created_by": created_by,
                "delivery_version": delivery_version,
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

        action = check_mcp_readiness_notification_receipt_action(d.pop("action"))

        reason = d.pop("reason")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        created_by = UUID(d.pop("created_by"))

        delivery_version = d.pop("delivery_version")

        _destination = d.pop("destination", UNSET)
        destination: MCPReadinessNotificationReceiptDestination | Unset
        if isinstance(_destination, Unset):
            destination = UNSET
        else:
            destination = check_mcp_readiness_notification_receipt_destination(_destination)

        mcp_readiness_notification_receipt = cls(
            request_id=request_id,
            event_id=event_id,
            action=action,
            reason=reason,
            created_at=created_at,
            created_by=created_by,
            delivery_version=delivery_version,
            destination=destination,
        )

        mcp_readiness_notification_receipt.additional_properties = d
        return mcp_readiness_notification_receipt

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
