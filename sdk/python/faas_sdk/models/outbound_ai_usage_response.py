from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

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
    daily_cost_millicents: int | Unset = UNSET
    daily_cost_limit_millicents: int | None | Unset = UNSET
    month: str | Unset = UNSET
    """Calendar month of usage_date as YYYY-MM."""
    monthly_cost_millicents: int | Unset = UNSET
    """Settled cost of the month through usage_date."""
    monthly_cost_limit_millicents: int | None | Unset = UNSET
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

        daily_cost_millicents = self.daily_cost_millicents

        daily_cost_limit_millicents: int | None | Unset
        if isinstance(self.daily_cost_limit_millicents, Unset):
            daily_cost_limit_millicents = UNSET
        else:
            daily_cost_limit_millicents = self.daily_cost_limit_millicents

        month = self.month

        monthly_cost_millicents = self.monthly_cost_millicents

        monthly_cost_limit_millicents: int | None | Unset
        if isinstance(self.monthly_cost_limit_millicents, Unset):
            monthly_cost_limit_millicents = UNSET
        else:
            monthly_cost_limit_millicents = self.monthly_cost_limit_millicents

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
        if daily_cost_millicents is not UNSET:
            field_dict["daily_cost_millicents"] = daily_cost_millicents
        if daily_cost_limit_millicents is not UNSET:
            field_dict["daily_cost_limit_millicents"] = daily_cost_limit_millicents
        if month is not UNSET:
            field_dict["month"] = month
        if monthly_cost_millicents is not UNSET:
            field_dict["monthly_cost_millicents"] = monthly_cost_millicents
        if monthly_cost_limit_millicents is not UNSET:
            field_dict["monthly_cost_limit_millicents"] = monthly_cost_limit_millicents

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

        daily_cost_millicents = d.pop("daily_cost_millicents", UNSET)

        def _parse_daily_cost_limit_millicents(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        daily_cost_limit_millicents = _parse_daily_cost_limit_millicents(d.pop("daily_cost_limit_millicents", UNSET))

        month = d.pop("month", UNSET)

        monthly_cost_millicents = d.pop("monthly_cost_millicents", UNSET)

        def _parse_monthly_cost_limit_millicents(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        monthly_cost_limit_millicents = _parse_monthly_cost_limit_millicents(
            d.pop("monthly_cost_limit_millicents", UNSET)
        )

        outbound_ai_usage_response = cls(
            usage_date=usage_date,
            ai_provider=ai_provider,
            daily_token_count=daily_token_count,
            daily_token_limit=daily_token_limit,
            resets_at=resets_at,
            items=items,
            daily_cost_millicents=daily_cost_millicents,
            daily_cost_limit_millicents=daily_cost_limit_millicents,
            month=month,
            monthly_cost_millicents=monthly_cost_millicents,
            monthly_cost_limit_millicents=monthly_cost_limit_millicents,
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
