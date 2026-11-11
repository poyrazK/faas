from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.update_notification_channel_request_pagerduty_region import (
    UpdateNotificationChannelRequestPagerdutyRegion,
    check_update_notification_channel_request_pagerduty_region,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="UpdateNotificationChannelRequest")


@_attrs_define
class UpdateNotificationChannelRequest:
    """Renames a channel or replaces its destination; set name, the channel's own destination, or both."""

    name: str | Unset = UNSET
    """New name, unique per account."""
    slack_webhook_url: str | Unset = UNSET
    """New Slack incoming webhook (kind slack)."""
    pagerduty_routing_key: str | Unset = UNSET
    """New Events API v2 integration key (kind pagerduty)."""
    pagerduty_region: UpdateNotificationChannelRequestPagerdutyRegion | Unset = UNSET
    """PagerDuty service region of the new key (kind pagerduty); default us."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        slack_webhook_url = self.slack_webhook_url

        pagerduty_routing_key = self.pagerduty_routing_key

        pagerduty_region: str | Unset = UNSET
        if not isinstance(self.pagerduty_region, Unset):
            pagerduty_region = self.pagerduty_region

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if name is not UNSET:
            field_dict["name"] = name
        if slack_webhook_url is not UNSET:
            field_dict["slack_webhook_url"] = slack_webhook_url
        if pagerduty_routing_key is not UNSET:
            field_dict["pagerduty_routing_key"] = pagerduty_routing_key
        if pagerduty_region is not UNSET:
            field_dict["pagerduty_region"] = pagerduty_region

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name", UNSET)

        slack_webhook_url = d.pop("slack_webhook_url", UNSET)

        pagerduty_routing_key = d.pop("pagerduty_routing_key", UNSET)

        _pagerduty_region = d.pop("pagerduty_region", UNSET)
        pagerduty_region: UpdateNotificationChannelRequestPagerdutyRegion | Unset
        if isinstance(_pagerduty_region, Unset):
            pagerduty_region = UNSET
        else:
            pagerduty_region = check_update_notification_channel_request_pagerduty_region(_pagerduty_region)

        update_notification_channel_request = cls(
            name=name,
            slack_webhook_url=slack_webhook_url,
            pagerduty_routing_key=pagerduty_routing_key,
            pagerduty_region=pagerduty_region,
        )

        update_notification_channel_request.additional_properties = d
        return update_notification_channel_request

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
