from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ConsumerCreditPack")


@_attrs_define
class ConsumerCreditPack:
    """A predefined amount of prepaid credit, sold for the same amount in Stripe Checkout."""

    id: str
    label: str
    currency: str
    amount_millicents: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        label = self.label

        currency = self.currency

        amount_millicents = self.amount_millicents

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "label": label,
                "currency": currency,
                "amount_millicents": amount_millicents,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        label = d.pop("label")

        currency = d.pop("currency")

        amount_millicents = d.pop("amount_millicents")

        consumer_credit_pack = cls(
            id=id,
            label=label,
            currency=currency,
            amount_millicents=amount_millicents,
        )

        consumer_credit_pack.additional_properties = d
        return consumer_credit_pack

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
