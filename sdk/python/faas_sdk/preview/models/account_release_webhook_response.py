from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.account_release_webhook_response_delivery_format import (
    AccountReleaseWebhookResponseDeliveryFormat,
    check_account_release_webhook_response_delivery_format,
)
from ..models.account_release_webhook_response_event_filter_item import (
    AccountReleaseWebhookResponseEventFilterItem,
    check_account_release_webhook_response_event_filter_item,
)
from ..models.account_release_webhook_response_retry_policy import (
    AccountReleaseWebhookResponseRetryPolicy,
    check_account_release_webhook_response_retry_policy,
)
from ..models.account_release_webhook_response_scope import (
    AccountReleaseWebhookResponseScope,
    check_account_release_webhook_response_scope,
)
from ..models.account_release_webhook_response_webhook_secret_sealed_masked import (
    AccountReleaseWebhookResponseWebhookSecretSealedMasked,
    check_account_release_webhook_response_webhook_secret_sealed_masked,
)

T = TypeVar("T", bound="AccountReleaseWebhookResponse")


@_attrs_define
class AccountReleaseWebhookResponse:
    """Account-owned release receiver; no app_id because it follows all current and future apps."""

    id: str
    scope: AccountReleaseWebhookResponseScope
    account_id: UUID
    target_url: str
    webhook_secret_sealed_masked: AccountReleaseWebhookResponseWebhookSecretSealedMasked
    event_filter: list[AccountReleaseWebhookResponseEventFilterItem]
    retry_policy: AccountReleaseWebhookResponseRetryPolicy
    delivery_format: AccountReleaseWebhookResponseDeliveryFormat
    enabled: bool
    created_at: datetime.datetime
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        scope: str = self.scope

        account_id = str(self.account_id)

        target_url = self.target_url

        webhook_secret_sealed_masked: str = self.webhook_secret_sealed_masked

        event_filter = []
        for event_filter_item_data in self.event_filter:
            event_filter_item: str = event_filter_item_data
            event_filter.append(event_filter_item)

        retry_policy: str = self.retry_policy

        delivery_format: str = self.delivery_format

        enabled = self.enabled

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "scope": scope,
                "account_id": account_id,
                "target_url": target_url,
                "webhook_secret_sealed_masked": webhook_secret_sealed_masked,
                "event_filter": event_filter,
                "retry_policy": retry_policy,
                "delivery_format": delivery_format,
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

        scope = check_account_release_webhook_response_scope(d.pop("scope"))

        account_id = UUID(d.pop("account_id"))

        target_url = d.pop("target_url")

        webhook_secret_sealed_masked = check_account_release_webhook_response_webhook_secret_sealed_masked(
            d.pop("webhook_secret_sealed_masked")
        )

        event_filter = []
        _event_filter = d.pop("event_filter")
        for event_filter_item_data in _event_filter:
            event_filter_item = check_account_release_webhook_response_event_filter_item(event_filter_item_data)

            event_filter.append(event_filter_item)

        retry_policy = check_account_release_webhook_response_retry_policy(d.pop("retry_policy"))

        delivery_format = check_account_release_webhook_response_delivery_format(d.pop("delivery_format"))

        enabled = d.pop("enabled")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        account_release_webhook_response = cls(
            id=id,
            scope=scope,
            account_id=account_id,
            target_url=target_url,
            webhook_secret_sealed_masked=webhook_secret_sealed_masked,
            event_filter=event_filter,
            retry_policy=retry_policy,
            delivery_format=delivery_format,
            enabled=enabled,
            created_at=created_at,
            updated_at=updated_at,
        )

        account_release_webhook_response.additional_properties = d
        return account_release_webhook_response

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
