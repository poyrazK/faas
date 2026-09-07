from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_webhook_response_event_filter_item import (
    AppWebhookResponseEventFilterItem,
    check_app_webhook_response_event_filter_item,
)
from ..models.app_webhook_response_retry_policy import (
    AppWebhookResponseRetryPolicy,
    check_app_webhook_response_retry_policy,
)
from ..models.app_webhook_response_webhook_secret_sealed_masked import (
    AppWebhookResponseWebhookSecretSealedMasked,
    check_app_webhook_response_webhook_secret_sealed_masked,
)

T = TypeVar("T", bound="AppWebhookResponse")


@_attrs_define
class AppWebhookResponse:
    """An outbound webhook subscription. Carries the masked HMAC secret;
    the sealed ciphertext is server-side only.

        Example:
            {'id': '0123456789abcdef0123456789abcdef', 'app_id': 'fedcba9876543210fedcba9876543210', 'account_id':
                '8b1f5e5d-273e-5a18-ae00-58fceba4fe6c', 'target_url': 'https://example.com/hook',
                'webhook_secret_sealed_masked': '***', 'event_filter': ['cron.fired', 'app.created'], 'retry_policy': 'default',
                'enabled': True, 'created_at': '2026-08-06T10:00:00Z', 'updated_at': '2026-08-06T10:00:00Z'}

    """

    id: str
    app_id: str
    account_id: UUID
    target_url: str
    webhook_secret_sealed_masked: AppWebhookResponseWebhookSecretSealedMasked
    event_filter: list[AppWebhookResponseEventFilterItem]
    retry_policy: AppWebhookResponseRetryPolicy
    enabled: bool
    created_at: datetime.datetime
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        app_id = self.app_id

        account_id = str(self.account_id)

        target_url = self.target_url

        webhook_secret_sealed_masked: str = self.webhook_secret_sealed_masked

        event_filter = []
        for event_filter_item_data in self.event_filter:
            event_filter_item: str = event_filter_item_data
            event_filter.append(event_filter_item)

        retry_policy: str = self.retry_policy

        enabled = self.enabled

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "account_id": account_id,
                "target_url": target_url,
                "webhook_secret_sealed_masked": webhook_secret_sealed_masked,
                "event_filter": event_filter,
                "retry_policy": retry_policy,
                "enabled": enabled,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        app_id = d.pop("app_id")

        account_id = UUID(d.pop("account_id"))

        target_url = d.pop("target_url")

        webhook_secret_sealed_masked = check_app_webhook_response_webhook_secret_sealed_masked(
            d.pop("webhook_secret_sealed_masked")
        )

        event_filter = []
        _event_filter = d.pop("event_filter")
        for event_filter_item_data in _event_filter:
            event_filter_item = check_app_webhook_response_event_filter_item(event_filter_item_data)

            event_filter.append(event_filter_item)

        retry_policy = check_app_webhook_response_retry_policy(d.pop("retry_policy"))

        enabled = d.pop("enabled")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        app_webhook_response = cls(
            id=id,
            app_id=app_id,
            account_id=account_id,
            target_url=target_url,
            webhook_secret_sealed_masked=webhook_secret_sealed_masked,
            event_filter=event_filter,
            retry_policy=retry_policy,
            enabled=enabled,
            created_at=created_at,
            updated_at=updated_at,
        )

        app_webhook_response.additional_properties = d
        return app_webhook_response

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
