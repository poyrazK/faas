from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.outbound_ai_usage_item import OutboundAIUsageItem


T = TypeVar("T", bound="OutboundAIUsageResponse")


@_attrs_define
class OutboundAIUsageResponse:
    """One UTC day of AI gateway usage for an integration."""

    usage_date: datetime.date
    ai_provider: str
    """Empty when AI handling is off."""
    daily_token_count: int
    """Settled admission counter for today; the rollup sum of input and output tokens for an earlier day."""
    daily_token_limit: int | None
    resets_at: datetime.datetime
    """End of usage_date in UTC."""
    items: list[OutboundAIUsageItem]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        usage_date = self.usage_date.isoformat()

        ai_provider = self.ai_provider

        daily_token_count = self.daily_token_count

        daily_token_limit: int | None
        daily_token_limit = self.daily_token_limit

        resets_at = self.resets_at.isoformat()

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "usage_date": usage_date,
                "ai_provider": ai_provider,
                "daily_token_count": daily_token_count,
                "daily_token_limit": daily_token_limit,
                "resets_at": resets_at,
                "items": items,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_ai_usage_item import OutboundAIUsageItem

        d = dict(src_dict)
        usage_date = datetime.date.fromisoformat(d.pop("usage_date"))

        ai_provider = d.pop("ai_provider")

        daily_token_count = d.pop("daily_token_count")

        def _parse_daily_token_limit(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        daily_token_limit = _parse_daily_token_limit(d.pop("daily_token_limit"))

        resets_at = datetime.datetime.fromisoformat(d.pop("resets_at"))

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = OutboundAIUsageItem.from_dict(items_item_data)

            items.append(items_item)

        outbound_ai_usage_response = cls(
            usage_date=usage_date,
            ai_provider=ai_provider,
            daily_token_count=daily_token_count,
            daily_token_limit=daily_token_limit,
            resets_at=resets_at,
            items=items,
        )

        outbound_ai_usage_response.additional_properties = d
        return outbound_ai_usage_response

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
