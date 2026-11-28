from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="SetFinancialBudgetChannelsRequest")


@_attrs_define
class SetFinancialBudgetChannelsRequest:
    """The full replacement set of notification channels for a budget's alerts."""

    channel_ids: list[UUID]
    """Account-owned notification channel ids; empty leaves only the account email."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        channel_ids = []
        for channel_ids_item_data in self.channel_ids:
            channel_ids_item = str(channel_ids_item_data)
            channel_ids.append(channel_ids_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
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

        set_financial_budget_channels_request = cls(
            channel_ids=channel_ids,
        )

        set_financial_budget_channels_request.additional_properties = d
        return set_financial_budget_channels_request

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
