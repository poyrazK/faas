from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.notification_channel_response import NotificationChannelResponse


T = TypeVar("T", bound="FinancialBudgetChannelsResponse")


@_attrs_define
class FinancialBudgetChannelsResponse:
    """The notification channels a budget's threshold alerts also go to, with each channel's latest delivery outcome."""

    policy_id: UUID
    channels: list[NotificationChannelResponse]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        policy_id = str(self.policy_id)

        channels = []
        for channels_item_data in self.channels:
            channels_item = channels_item_data.to_dict()
            channels.append(channels_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "policy_id": policy_id,
                "channels": channels,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.notification_channel_response import NotificationChannelResponse

        d = dict(src_dict)
        policy_id = UUID(d.pop("policy_id"))

        channels = []
        _channels = d.pop("channels")
        for channels_item_data in _channels:
            channels_item = NotificationChannelResponse.from_dict(channels_item_data)

            channels.append(channels_item)

        financial_budget_channels_response = cls(
            policy_id=policy_id,
            channels=channels,
        )

        financial_budget_channels_response.additional_properties = d
        return financial_budget_channels_response

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
