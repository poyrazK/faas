from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_webhook_delivery_response_event import (
    AppWebhookDeliveryResponseEvent,
    check_app_webhook_delivery_response_event,
)
from ..models.app_webhook_delivery_response_status import (
    AppWebhookDeliveryResponseStatus,
    check_app_webhook_delivery_response_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_webhook_delivery_response_payload import AppWebhookDeliveryResponsePayload


T = TypeVar("T", bound="AppWebhookDeliveryResponse")


@_attrs_define
class AppWebhookDeliveryResponse:
    """One row per (event × target) emission. The dispatcher mutates
    this row in place as attempts progress; the GET /deliveries
    endpoint returns this shape per row. attempt=7 + status='dead'
    is the DLQ state; the customer-facing retry endpoint flips a
    dead row back to status='pending' for one more shot.

        Example:
            {'id': '0123456789abcdef0123456789abcdef', 'webhook_id': 'fedcba9876543210fedcba9876543210', 'app_id':
                '8b1f5e5d273e5a18ae0058fceba4fe6c', 'account_id': '8b1f5e5d-273e-5a18-ae00-58fceba4fe6c', 'event': 'cron.fired',
                'payload': {'cron_name': 'nightly-rollup'}, 'attempt': 0, 'status': 'pending', 'next_attempt_at':
                '2026-08-06T10:00:00Z', 'created_at': '2026-08-06T10:00:00Z', 'updated_at': '2026-08-06T10:00:00Z'}

    """

    id: str
    webhook_id: str
    app_id: str
    account_id: UUID
    event: AppWebhookDeliveryResponseEvent
    attempt: int
    status: AppWebhookDeliveryResponseStatus
    next_attempt_at: datetime.datetime
    created_at: datetime.datetime
    updated_at: datetime.datetime
    payload: AppWebhookDeliveryResponsePayload | Unset = UNSET
    """The original event payload (omitted on rows past the first attempt; the customer has already seen it)."""
    last_error: str | Unset = UNSET
    last_response_code: int | Unset = UNSET
    delivered_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        webhook_id = self.webhook_id

        app_id = self.app_id

        account_id = str(self.account_id)

        event: str = self.event

        attempt = self.attempt

        status: str = self.status

        next_attempt_at = self.next_attempt_at.isoformat()

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        payload: dict[str, Any] | Unset = UNSET
        if not isinstance(self.payload, Unset):
            payload = self.payload.to_dict()

        last_error = self.last_error

        last_response_code = self.last_response_code

        delivered_at: str | Unset = UNSET
        if not isinstance(self.delivered_at, Unset):
            delivered_at = self.delivered_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "webhook_id": webhook_id,
                "app_id": app_id,
                "account_id": account_id,
                "event": event,
                "attempt": attempt,
                "status": status,
                "next_attempt_at": next_attempt_at,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if payload is not UNSET:
            field_dict["payload"] = payload
        if last_error is not UNSET:
            field_dict["last_error"] = last_error
        if last_response_code is not UNSET:
            field_dict["last_response_code"] = last_response_code
        if delivered_at is not UNSET:
            field_dict["delivered_at"] = delivered_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_webhook_delivery_response_payload import AppWebhookDeliveryResponsePayload

        d = dict(src_dict)
        id = d.pop("id")

        webhook_id = d.pop("webhook_id")

        app_id = d.pop("app_id")

        account_id = UUID(d.pop("account_id"))

        event = check_app_webhook_delivery_response_event(d.pop("event"))

        attempt = d.pop("attempt")

        status = check_app_webhook_delivery_response_status(d.pop("status"))

        next_attempt_at = datetime.datetime.fromisoformat(d.pop("next_attempt_at"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _payload = d.pop("payload", UNSET)
        payload: AppWebhookDeliveryResponsePayload | Unset
        if isinstance(_payload, Unset):
            payload = UNSET
        else:
            payload = AppWebhookDeliveryResponsePayload.from_dict(_payload)

        last_error = d.pop("last_error", UNSET)

        last_response_code = d.pop("last_response_code", UNSET)

        _delivered_at = d.pop("delivered_at", UNSET)
        delivered_at: datetime.datetime | Unset
        if isinstance(_delivered_at, Unset):
            delivered_at = UNSET
        else:
            delivered_at = datetime.datetime.fromisoformat(_delivered_at)

        app_webhook_delivery_response = cls(
            id=id,
            webhook_id=webhook_id,
            app_id=app_id,
            account_id=account_id,
            event=event,
            attempt=attempt,
            status=status,
            next_attempt_at=next_attempt_at,
            created_at=created_at,
            updated_at=updated_at,
            payload=payload,
            last_error=last_error,
            last_response_code=last_response_code,
            delivered_at=delivered_at,
        )

        app_webhook_delivery_response.additional_properties = d
        return app_webhook_delivery_response

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
