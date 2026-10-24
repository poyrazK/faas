from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.api_consumer_credit_balance import APIConsumerCreditBalance
    from ..models.api_consumer_credit_response import APIConsumerCreditResponse


T = TypeVar("T", bound="APIConsumerCreditListResponse")


@_attrs_define
class APIConsumerCreditListResponse:
    """A consumer's credit grants, newest first, with balances per currency."""

    credits_: list[APIConsumerCreditResponse]
    balances: list[APIConsumerCreditBalance]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        credits_ = []
        for credits_item_data in self.credits_:
            credits_item = credits_item_data.to_dict()
            credits_.append(credits_item)

        balances = []
        for balances_item_data in self.balances:
            balances_item = balances_item_data.to_dict()
            balances.append(balances_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "credits": credits_,
                "balances": balances,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_consumer_credit_balance import APIConsumerCreditBalance
        from ..models.api_consumer_credit_response import APIConsumerCreditResponse

        d = dict(src_dict)
        credits_ = []
        _credits_ = d.pop("credits")
        for credits_item_data in _credits_:
            credits_item = APIConsumerCreditResponse.from_dict(credits_item_data)

            credits_.append(credits_item)

        balances = []
        _balances = d.pop("balances")
        for balances_item_data in _balances:
            balances_item = APIConsumerCreditBalance.from_dict(balances_item_data)

            balances.append(balances_item)

        api_consumer_credit_list_response = cls(
            credits_=credits_,
            balances=balances,
        )

        api_consumer_credit_list_response.additional_properties = d
        return api_consumer_credit_list_response

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
