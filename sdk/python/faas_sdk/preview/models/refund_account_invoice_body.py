from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RefundAccountInvoiceBody")


@_attrs_define
class RefundAccountInvoiceBody:
    invoice_id: UUID
    """Local Gregale invoice UUID."""
    amount_cents: int
    """Refund amount in EUR cents."""
    reason: str
    """Reason recorded with the money-moving operation."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        invoice_id = str(self.invoice_id)

        amount_cents = self.amount_cents

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "invoice_id": invoice_id,
                "amount_cents": amount_cents,
                "reason": reason,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        invoice_id = UUID(d.pop("invoice_id"))

        amount_cents = d.pop("amount_cents")

        reason = d.pop("reason")

        refund_account_invoice_body = cls(
            invoice_id=invoice_id,
            amount_cents=amount_cents,
            reason=reason,
        )

        refund_account_invoice_body.additional_properties = d
        return refund_account_invoice_body

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
