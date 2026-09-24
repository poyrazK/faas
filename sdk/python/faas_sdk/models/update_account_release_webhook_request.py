from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.update_account_release_webhook_request_delivery_format import (
    UpdateAccountReleaseWebhookRequestDeliveryFormat,
    check_update_account_release_webhook_request_delivery_format,
)
from ..models.update_account_release_webhook_request_event_filter_item import (
    UpdateAccountReleaseWebhookRequestEventFilterItem,
    check_update_account_release_webhook_request_event_filter_item,
)
from ..models.update_account_release_webhook_request_retry_policy import (
    UpdateAccountReleaseWebhookRequestRetryPolicy,
    check_update_account_release_webhook_request_retry_policy,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="UpdateAccountReleaseWebhookRequest")


@_attrs_define
class UpdateAccountReleaseWebhookRequest:
    """Partial update; omitted fields retain their current value."""

    target_url: str | Unset = UNSET
    webhook_secret: str | Unset = UNSET
    event_filter: list[UpdateAccountReleaseWebhookRequestEventFilterItem] | Unset = UNSET
    retry_policy: UpdateAccountReleaseWebhookRequestRetryPolicy | Unset = UNSET
    delivery_format: UpdateAccountReleaseWebhookRequestDeliveryFormat | Unset = UNSET
    enabled: bool | Unset = UNSET
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

        delivery_format: str | Unset = UNSET
        if not isinstance(self.delivery_format, Unset):
            delivery_format = self.delivery_format

        enabled = self.enabled

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if target_url is not UNSET:
            field_dict["target_url"] = target_url
        if webhook_secret is not UNSET:
            field_dict["webhook_secret"] = webhook_secret
        if event_filter is not UNSET:
            field_dict["event_filter"] = event_filter
        if retry_policy is not UNSET:
            field_dict["retry_policy"] = retry_policy
        if delivery_format is not UNSET:
            field_dict["delivery_format"] = delivery_format
        if enabled is not UNSET:
            field_dict["enabled"] = enabled

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        target_url = d.pop("target_url", UNSET)

        webhook_secret = d.pop("webhook_secret", UNSET)

        _event_filter = d.pop("event_filter", UNSET)
        event_filter: list[UpdateAccountReleaseWebhookRequestEventFilterItem] | Unset = UNSET
        if _event_filter is not UNSET:
            event_filter = []
            for event_filter_item_data in _event_filter:
                event_filter_item = check_update_account_release_webhook_request_event_filter_item(
                    event_filter_item_data
                )

                event_filter.append(event_filter_item)

        _retry_policy = d.pop("retry_policy", UNSET)
        retry_policy: UpdateAccountReleaseWebhookRequestRetryPolicy | Unset
        if isinstance(_retry_policy, Unset):
            retry_policy = UNSET
        else:
            retry_policy = check_update_account_release_webhook_request_retry_policy(_retry_policy)

        _delivery_format = d.pop("delivery_format", UNSET)
        delivery_format: UpdateAccountReleaseWebhookRequestDeliveryFormat | Unset
        if isinstance(_delivery_format, Unset):
            delivery_format = UNSET
        else:
            delivery_format = check_update_account_release_webhook_request_delivery_format(_delivery_format)

        enabled = d.pop("enabled", UNSET)

        update_account_release_webhook_request = cls(
            target_url=target_url,
            webhook_secret=webhook_secret,
            event_filter=event_filter,
            retry_policy=retry_policy,
            delivery_format=delivery_format,
            enabled=enabled,
        )

        update_account_release_webhook_request.additional_properties = d
        return update_account_release_webhook_request

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
