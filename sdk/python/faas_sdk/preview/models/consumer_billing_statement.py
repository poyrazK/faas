from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.consumer_billing_invoice import ConsumerBillingInvoice


T = TypeVar("T", bound="ConsumerBillingStatement")


@_attrs_define
class ConsumerBillingStatement:
    """One finalized statement as its consumer sees it."""

    id: UUID
    period_start: datetime.datetime
    period_end: datetime.datetime
    revision: int
    currency: str
    billable_units: int
    amount_millicents: int
    credit_applied_millicents: int
    """Prepaid credit drawn when this billing statement was finalized."""
    amount_due_millicents: int
    """amount_millicents less credit_applied_millicents: what is left to pay."""
    finalized_at: datetime.datetime | Unset = UNSET
    invoice: ConsumerBillingInvoice | Unset = UNSET
    """The customer-facing view of a statement's exported invoice."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        period_start = self.period_start.isoformat()

        period_end = self.period_end.isoformat()

        revision = self.revision

        currency = self.currency

        billable_units = self.billable_units

        amount_millicents = self.amount_millicents

        credit_applied_millicents = self.credit_applied_millicents

        amount_due_millicents = self.amount_due_millicents

        finalized_at: str | Unset = UNSET
        if not isinstance(self.finalized_at, Unset):
            finalized_at = self.finalized_at.isoformat()

        invoice: dict[str, Any] | Unset = UNSET
        if not isinstance(self.invoice, Unset):
            invoice = self.invoice.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "period_start": period_start,
                "period_end": period_end,
                "revision": revision,
                "currency": currency,
                "billable_units": billable_units,
                "amount_millicents": amount_millicents,
                "credit_applied_millicents": credit_applied_millicents,
                "amount_due_millicents": amount_due_millicents,
            }
        )
        if finalized_at is not UNSET:
            field_dict["finalized_at"] = finalized_at
        if invoice is not UNSET:
            field_dict["invoice"] = invoice

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.consumer_billing_invoice import ConsumerBillingInvoice

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        period_start = datetime.datetime.fromisoformat(d.pop("period_start"))

        period_end = datetime.datetime.fromisoformat(d.pop("period_end"))

        revision = d.pop("revision")

        currency = d.pop("currency")

        billable_units = d.pop("billable_units")

        amount_millicents = d.pop("amount_millicents")

        credit_applied_millicents = d.pop("credit_applied_millicents")

        amount_due_millicents = d.pop("amount_due_millicents")

        _finalized_at = d.pop("finalized_at", UNSET)
        finalized_at: datetime.datetime | Unset
        if isinstance(_finalized_at, Unset):
            finalized_at = UNSET
        else:
            finalized_at = datetime.datetime.fromisoformat(_finalized_at)

        _invoice = d.pop("invoice", UNSET)
        invoice: ConsumerBillingInvoice | Unset
        if isinstance(_invoice, Unset):
            invoice = UNSET
        else:
            invoice = ConsumerBillingInvoice.from_dict(_invoice)

        consumer_billing_statement = cls(
            id=id,
            period_start=period_start,
            period_end=period_end,
            revision=revision,
            currency=currency,
            billable_units=billable_units,
            amount_millicents=amount_millicents,
            credit_applied_millicents=credit_applied_millicents,
            amount_due_millicents=amount_due_millicents,
            finalized_at=finalized_at,
            invoice=invoice,
        )

        consumer_billing_statement.additional_properties = d
        return consumer_billing_statement

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
