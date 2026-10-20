from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.consumed_credit_row import ConsumedCreditRow


T = TypeVar("T", bound="ConsumeInvoiceResponse")


@_attrs_define
class ConsumeInvoiceResponse:
    """Returned by POST /v1/invoices/{id}/consume-credits (issue
    #279 PR-C). consumed_cents is the floored integer cents of
    overage drained against this invoice. remaining_credits_cents
    is the sum of cents_remaining across the account's active
    credits after the call. already_consumed_for_invoice is true
    on idempotent replays (the reducer returns the same
    consumed_cents without double-decrementing). per_credit lists
    FIFO-ordered credit drains with their post-decrement balance.
    Money is integer cents (CLAUDE.md).

    """

    invoice_id: UUID
    consumed_cents: int
    remaining_credits_cents: int
    already_consumed_for_invoice: bool
    per_credit: list[ConsumedCreditRow]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        invoice_id = str(self.invoice_id)

        consumed_cents = self.consumed_cents

        remaining_credits_cents = self.remaining_credits_cents

        already_consumed_for_invoice = self.already_consumed_for_invoice

        per_credit = []
        for per_credit_item_data in self.per_credit:
            per_credit_item = per_credit_item_data.to_dict()
            per_credit.append(per_credit_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "invoice_id": invoice_id,
                "consumed_cents": consumed_cents,
                "remaining_credits_cents": remaining_credits_cents,
                "already_consumed_for_invoice": already_consumed_for_invoice,
                "per_credit": per_credit,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.consumed_credit_row import ConsumedCreditRow

        d = dict(src_dict)
        invoice_id = UUID(d.pop("invoice_id"))

        consumed_cents = d.pop("consumed_cents")

        remaining_credits_cents = d.pop("remaining_credits_cents")

        already_consumed_for_invoice = d.pop("already_consumed_for_invoice")

        per_credit = []
        _per_credit = d.pop("per_credit")
        for per_credit_item_data in _per_credit:
            per_credit_item = ConsumedCreditRow.from_dict(per_credit_item_data)

            per_credit.append(per_credit_item)

        consume_invoice_response = cls(
            invoice_id=invoice_id,
            consumed_cents=consumed_cents,
            remaining_credits_cents=remaining_credits_cents,
            already_consumed_for_invoice=already_consumed_for_invoice,
            per_credit=per_credit,
        )

        consume_invoice_response.additional_properties = d
        return consume_invoice_response

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
