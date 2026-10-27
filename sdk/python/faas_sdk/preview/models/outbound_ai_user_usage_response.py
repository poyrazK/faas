from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.outbound_ai_user_usage_item import OutboundAIUserUsageItem


T = TypeVar("T", bound="OutboundAIUserUsageResponse")


@_attrs_define
class OutboundAIUserUsageResponse:
    """One UTC day of per-end-user AI usage, most expensive first."""

    usage_date: datetime.date
    user_daily_token_limit: int | None
    user_daily_cost_limit_millicents: int | None
    tracked_users: int
    """Distinct end-user rows of the day, including "~other"."""
    items: list[OutboundAIUserUsageItem]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        usage_date = self.usage_date.isoformat()

        user_daily_token_limit: int | None
        user_daily_token_limit = self.user_daily_token_limit

        user_daily_cost_limit_millicents: int | None
        user_daily_cost_limit_millicents = self.user_daily_cost_limit_millicents

        tracked_users = self.tracked_users

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "usage_date": usage_date,
                "user_daily_token_limit": user_daily_token_limit,
                "user_daily_cost_limit_millicents": user_daily_cost_limit_millicents,
                "tracked_users": tracked_users,
                "items": items,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_ai_user_usage_item import OutboundAIUserUsageItem

        d = dict(src_dict)
        usage_date = datetime.date.fromisoformat(d.pop("usage_date"))

        def _parse_user_daily_token_limit(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        user_daily_token_limit = _parse_user_daily_token_limit(d.pop("user_daily_token_limit"))

        def _parse_user_daily_cost_limit_millicents(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        user_daily_cost_limit_millicents = _parse_user_daily_cost_limit_millicents(
            d.pop("user_daily_cost_limit_millicents")
        )

        tracked_users = d.pop("tracked_users")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = OutboundAIUserUsageItem.from_dict(items_item_data)

            items.append(items_item)

        outbound_ai_user_usage_response = cls(
            usage_date=usage_date,
            user_daily_token_limit=user_daily_token_limit,
            user_daily_cost_limit_millicents=user_daily_cost_limit_millicents,
            tracked_users=tracked_users,
            items=items,
        )

        outbound_ai_user_usage_response.additional_properties = d
        return outbound_ai_user_usage_response

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
