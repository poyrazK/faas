from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="FlagNotificationSettings")


@_attrs_define
class FlagNotificationSettings:
    """An environment's flag notification channels."""

    channel_ids: list[UUID]
    """Notification channel IDs; empty when the environment's flag notifications are off."""
    updated_by: str | Unset = UNSET
    """The account that last changed them."""
    updated_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        channel_ids = []
        for channel_ids_item_data in self.channel_ids:
            channel_ids_item = str(channel_ids_item_data)
            channel_ids.append(channel_ids_item)

        updated_by = self.updated_by

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "channel_ids": channel_ids,
            }
        )
        if updated_by is not UNSET:
            field_dict["updated_by"] = updated_by
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        channel_ids = []
        _channel_ids = d.pop("channel_ids")
        for channel_ids_item_data in _channel_ids:
            channel_ids_item = UUID(channel_ids_item_data)

            channel_ids.append(channel_ids_item)

        updated_by = d.pop("updated_by", UNSET)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        flag_notification_settings = cls(
            channel_ids=channel_ids,
            updated_by=updated_by,
            updated_at=updated_at,
        )

        flag_notification_settings.additional_properties = d
        return flag_notification_settings

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
