from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.consumer_signup_price import ConsumerSignupPrice


T = TypeVar("T", bound="ConsumerSignupPlan")


@_attrs_define
class ConsumerSignupPlan:
    """A plan customers can sign up for; no id is the app's default plan."""

    name: str
    prices: list[ConsumerSignupPrice]
    id: UUID | Unset = UNSET
    max_requests_per_minute: int | Unset = UNSET
    max_units_per_month: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        prices = []
        for prices_item_data in self.prices:
            prices_item = prices_item_data.to_dict()
            prices.append(prices_item)

        id: str | Unset = UNSET
        if not isinstance(self.id, Unset):
            id = str(self.id)

        max_requests_per_minute = self.max_requests_per_minute

        max_units_per_month = self.max_units_per_month

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "prices": prices,
            }
        )
        if id is not UNSET:
            field_dict["id"] = id
        if max_requests_per_minute is not UNSET:
            field_dict["max_requests_per_minute"] = max_requests_per_minute
        if max_units_per_month is not UNSET:
            field_dict["max_units_per_month"] = max_units_per_month

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.consumer_signup_price import ConsumerSignupPrice

        d = dict(src_dict)
        name = d.pop("name")

        prices = []
        _prices = d.pop("prices")
        for prices_item_data in _prices:
            prices_item = ConsumerSignupPrice.from_dict(prices_item_data)

            prices.append(prices_item)

        _id = d.pop("id", UNSET)
        id: UUID | Unset
        if isinstance(_id, Unset):
            id = UNSET
        else:
            id = UUID(_id)

        max_requests_per_minute = d.pop("max_requests_per_minute", UNSET)

        max_units_per_month = d.pop("max_units_per_month", UNSET)

        consumer_signup_plan = cls(
            name=name,
            prices=prices,
            id=id,
            max_requests_per_minute=max_requests_per_minute,
            max_units_per_month=max_units_per_month,
        )

        consumer_signup_plan.additional_properties = d
        return consumer_signup_plan

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
