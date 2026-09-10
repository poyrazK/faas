from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_app_webhook_request_event_filter_item import (
    CreateAppWebhookRequestEventFilterItem,
    check_create_app_webhook_request_event_filter_item,
)
from ..models.create_app_webhook_request_retry_policy import (
    CreateAppWebhookRequestRetryPolicy,
    check_create_app_webhook_request_retry_policy,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateAppWebhookRequest")


@_attrs_define
class CreateAppWebhookRequest:
    """Subscribe a target URL to events emitted by the app. The
    webhook_secret is HMAC-SHA256 sealed at rest with the host
    X25519 recipient (namespace `APP_WEBHOOK`).

        Example:
            {'target_url': 'https://example.com/hook', 'webhook_secret': 'shh', 'event_filter': ['cron.fired',
                'app.created'], 'retry_policy': 'default', 'enabled': True}

    """

    target_url: str
    webhook_secret: str
    event_filter: list[CreateAppWebhookRequestEventFilterItem] | Unset = UNSET
    retry_policy: CreateAppWebhookRequestRetryPolicy | Unset = "default"
    enabled: bool | Unset = True
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        target_url = self.target_url

        webhook_secret = self.webhook_secret

        event_filter: list[str] | Unset = UNSET
        if not isinstance(self.event_filter, Unset):
            event_filter = []
            for event_filter_item_data in self.event_filter:
                event_filter_item: str = event_filter_item_data
                event_filter.append(event_filter_item)

        retry_policy: str | Unset = UNSET
        if not isinstance(self.retry_policy, Unset):
            retry_policy = self.retry_policy

        enabled = self.enabled

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "target_url": target_url,
                "webhook_secret": webhook_secret,
            }
        )
        if event_filter is not UNSET:
            field_dict["event_filter"] = event_filter
        if retry_policy is not UNSET:
            field_dict["retry_policy"] = retry_policy
        if enabled is not UNSET:
            field_dict["enabled"] = enabled

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        target_url = d.pop("target_url")

        webhook_secret = d.pop("webhook_secret")

        _event_filter = d.pop("event_filter", UNSET)
        event_filter: list[CreateAppWebhookRequestEventFilterItem] | Unset = UNSET
        if _event_filter is not UNSET:
            event_filter = []
            for event_filter_item_data in _event_filter:
                event_filter_item = check_create_app_webhook_request_event_filter_item(event_filter_item_data)

                event_filter.append(event_filter_item)

        _retry_policy = d.pop("retry_policy", UNSET)
        retry_policy: CreateAppWebhookRequestRetryPolicy | Unset
        if isinstance(_retry_policy, Unset):
            retry_policy = UNSET
        else:
            retry_policy = check_create_app_webhook_request_retry_policy(_retry_policy)

        enabled = d.pop("enabled", UNSET)

        create_app_webhook_request = cls(
            target_url=target_url,
            webhook_secret=webhook_secret,
            event_filter=event_filter,
            retry_policy=retry_policy,
            enabled=enabled,
        )

        create_app_webhook_request.additional_properties = d
        return create_app_webhook_request

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
