from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ConsumerBillingSpendCap")


@_attrs_define
class ConsumerBillingSpendCap:
    """The consumer's monthly spend cap; once the amount due passes it, requests are refused until the month ends or credit
    is added.

    """

    currency: str
    monthly_cap_millicents: int
    capped: bool
    amount_due_millicents: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        currency = self.currency

        monthly_cap_millicents = self.monthly_cap_millicents

        capped = self.capped

        amount_due_millicents = self.amount_due_millicents

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "currency": currency,
                "monthly_cap_millicents": monthly_cap_millicents,
                "capped": capped,
            }
        )
        if amount_due_millicents is not UNSET:
            field_dict["amount_due_millicents"] = amount_due_millicents

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        currency = d.pop("currency")

        monthly_cap_millicents = d.pop("monthly_cap_millicents")

        capped = d.pop("capped")

        amount_due_millicents = d.pop("amount_due_millicents", UNSET)

        consumer_billing_spend_cap = cls(
            currency=currency,
            monthly_cap_millicents=monthly_cap_millicents,
            capped=capped,
            amount_due_millicents=amount_due_millicents,
        )

        consumer_billing_spend_cap.additional_properties = d
        return consumer_billing_spend_cap

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
