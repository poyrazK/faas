from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ConsumedCreditRow")


@_attrs_define
class ConsumedCreditRow:
    """One FIFO drain row inside ConsumeInvoiceResponse.per_credit.
    delta_cents is the negative-cents decrement applied to the
    credit; new_balance is cents_remaining after the call.

    """

    credit_id: UUID
    delta_cents: int
    """Negative integer cents applied to the credit."""
    new_balance: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        credit_id = str(self.credit_id)

        delta_cents = self.delta_cents

        new_balance = self.new_balance

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "credit_id": credit_id,
                "delta_cents": delta_cents,
                "new_balance": new_balance,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        credit_id = UUID(d.pop("credit_id"))

        delta_cents = d.pop("delta_cents")

        new_balance = d.pop("new_balance")

        consumed_credit_row = cls(
            credit_id=credit_id,
            delta_cents=delta_cents,
            new_balance=new_balance,
        )

        consumed_credit_row.additional_properties = d
        return consumed_credit_row

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
