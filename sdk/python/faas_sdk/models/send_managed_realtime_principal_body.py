from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.send_managed_realtime_principal_body_delivery import (
    SendManagedRealtimePrincipalBodyDelivery,
    check_send_managed_realtime_principal_body_delivery,
)
from ..models.send_managed_realtime_principal_body_notification_priority import (
    SendManagedRealtimePrincipalBodyNotificationPriority,
    check_send_managed_realtime_principal_body_notification_priority,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="SendManagedRealtimePrincipalBody")


@_attrs_define
class SendManagedRealtimePrincipalBody:
    principal: str
    data_base64: str
    """Standard base64; at most 4096 decoded bytes."""
    binary: bool | Unset = False
    delivery: SendManagedRealtimePrincipalBodyDelivery | Unset = "live"
    message_id: str | Unset = UNSET
    request_receipt: bool | Unset = False
    fallback_after_seconds: int | Unset = 0
    """Requires retained delivery; zero disables fallback."""
    notification_group_key: str | Unset = UNSET
    """Stable conversation, job or project key; requires a fallback."""
    notification_not_before: datetime.datetime | Unset = UNSET
    """Earliest built-in push delivery time; RFC3339 with timezone, at most 48 hours ahead. Requires fallback;
    explicit TTL must extend past this instant."""
    notification_collapse_key: str | Unset = UNSET
    """Requires fallback. Newer queued alerts supersede older alerts for the same recipient, device, category,
    priority and collapse key."""
    notification_ttl_seconds: int | Unset = UNSET
    """Push lifetime in seconds from publication. Positive values require fallback and must exceed its deadline.
    Zero or omission keeps default expiration."""
    notification_priority: SendManagedRealtimePrincipalBodyNotificationPriority | Unset = UNSET
    """Defaults to normal when omitted; requires retained fallback. Urgent bypass requires user opt-in."""
    notification_group_label: str | Unset = UNSET
    """Display name for summaries; requires a group key."""
    notification_category: str | Unset = UNSET
    """Requires a nonzero fallback deadline; omitted values use notifications for push preferences."""

    def to_dict(self) -> dict[str, Any]:
        principal = self.principal

        data_base64 = self.data_base64

        binary = self.binary

        delivery: str | Unset = UNSET
        if not isinstance(self.delivery, Unset):
            delivery = self.delivery

        message_id = self.message_id

        request_receipt = self.request_receipt

        fallback_after_seconds = self.fallback_after_seconds

        notification_group_key = self.notification_group_key

        notification_not_before: str | Unset = UNSET
        if not isinstance(self.notification_not_before, Unset):
            notification_not_before = self.notification_not_before.isoformat()

        notification_collapse_key = self.notification_collapse_key

        notification_ttl_seconds = self.notification_ttl_seconds

        notification_priority: str | Unset = UNSET
        if not isinstance(self.notification_priority, Unset):
            notification_priority = self.notification_priority

        notification_group_label = self.notification_group_label

        notification_category = self.notification_category

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "principal": principal,
                "data_base64": data_base64,
            }
        )
        if binary is not UNSET:
            field_dict["binary"] = binary
        if delivery is not UNSET:
            field_dict["delivery"] = delivery
        if message_id is not UNSET:
            field_dict["message_id"] = message_id
        if request_receipt is not UNSET:
            field_dict["request_receipt"] = request_receipt
        if fallback_after_seconds is not UNSET:
            field_dict["fallback_after_seconds"] = fallback_after_seconds
        if notification_group_key is not UNSET:
            field_dict["notification_group_key"] = notification_group_key
        if notification_not_before is not UNSET:
            field_dict["notification_not_before"] = notification_not_before
        if notification_collapse_key is not UNSET:
            field_dict["notification_collapse_key"] = notification_collapse_key
        if notification_ttl_seconds is not UNSET:
            field_dict["notification_ttl_seconds"] = notification_ttl_seconds
        if notification_priority is not UNSET:
            field_dict["notification_priority"] = notification_priority
        if notification_group_label is not UNSET:
            field_dict["notification_group_label"] = notification_group_label
        if notification_category is not UNSET:
            field_dict["notification_category"] = notification_category

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        principal = d.pop("principal")

        data_base64 = d.pop("data_base64")

        binary = d.pop("binary", UNSET)

        _delivery = d.pop("delivery", UNSET)
        delivery: SendManagedRealtimePrincipalBodyDelivery | Unset
        if isinstance(_delivery, Unset):
            delivery = UNSET
        else:
            delivery = check_send_managed_realtime_principal_body_delivery(_delivery)

        message_id = d.pop("message_id", UNSET)

        request_receipt = d.pop("request_receipt", UNSET)

        fallback_after_seconds = d.pop("fallback_after_seconds", UNSET)

        notification_group_key = d.pop("notification_group_key", UNSET)

        _notification_not_before = d.pop("notification_not_before", UNSET)
        notification_not_before: datetime.datetime | Unset
        if isinstance(_notification_not_before, Unset):
            notification_not_before = UNSET
        else:
            notification_not_before = datetime.datetime.fromisoformat(_notification_not_before)

        notification_collapse_key = d.pop("notification_collapse_key", UNSET)

        notification_ttl_seconds = d.pop("notification_ttl_seconds", UNSET)

        _notification_priority = d.pop("notification_priority", UNSET)
        notification_priority: SendManagedRealtimePrincipalBodyNotificationPriority | Unset
        if isinstance(_notification_priority, Unset):
            notification_priority = UNSET
        else:
            notification_priority = check_send_managed_realtime_principal_body_notification_priority(
                _notification_priority
            )

        notification_group_label = d.pop("notification_group_label", UNSET)

        notification_category = d.pop("notification_category", UNSET)

        send_managed_realtime_principal_body = cls(
            principal=principal,
            data_base64=data_base64,
            binary=binary,
            delivery=delivery,
            message_id=message_id,
            request_receipt=request_receipt,
            fallback_after_seconds=fallback_after_seconds,
            notification_group_key=notification_group_key,
            notification_not_before=notification_not_before,
            notification_collapse_key=notification_collapse_key,
            notification_ttl_seconds=notification_ttl_seconds,
            notification_priority=notification_priority,
            notification_group_label=notification_group_label,
            notification_category=notification_category,
        )

        return send_managed_realtime_principal_body
