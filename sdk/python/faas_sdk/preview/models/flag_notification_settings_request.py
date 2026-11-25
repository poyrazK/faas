from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="FlagNotificationSettingsRequest")


@_attrs_define
class FlagNotificationSettingsRequest:
    """New flag notification channels for an environment."""

    channel_ids: list[UUID]
    """Up to 5 of the account's notification channels; empty turns notifications off."""

    def to_dict(self) -> dict[str, Any]:
        channel_ids = []
        for channel_ids_item_data in self.channel_ids:
            channel_ids_item = str(channel_ids_item_data)
            channel_ids.append(channel_ids_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "channel_ids": channel_ids,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        channel_ids = []
        _channel_ids = d.pop("channel_ids")
        for channel_ids_item_data in _channel_ids:
            channel_ids_item = UUID(channel_ids_item_data)

            channel_ids.append(channel_ids_item)

        flag_notification_settings_request = cls(
            channel_ids=channel_ids,
        )

        return flag_notification_settings_request
