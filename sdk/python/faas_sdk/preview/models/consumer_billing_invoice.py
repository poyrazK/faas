from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.consumer_billing_invoice_payment_status import (
    ConsumerBillingInvoicePaymentStatus,
    check_consumer_billing_invoice_payment_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ConsumerBillingInvoice")


@_attrs_define
class ConsumerBillingInvoice:
    """The customer-facing view of a statement's exported invoice."""

    amount_minor: int
    payment_status: ConsumerBillingInvoicePaymentStatus | Unset = UNSET
    amount_remaining_minor: int | Unset = UNSET
    due_at: datetime.datetime | Unset = UNSET
    paid_at: datetime.datetime | Unset = UNSET
    hosted_invoice_url: str | Unset = UNSET
    """Stripe's page where the customer views and pays the invoice."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        amount_minor = self.amount_minor

        payment_status: str | Unset = UNSET
        if not isinstance(self.payment_status, Unset):
            payment_status = self.payment_status

        amount_remaining_minor = self.amount_remaining_minor

        due_at: str | Unset = UNSET
        if not isinstance(self.due_at, Unset):
            due_at = self.due_at.isoformat()

        paid_at: str | Unset = UNSET
        if not isinstance(self.paid_at, Unset):
            paid_at = self.paid_at.isoformat()

        hosted_invoice_url = self.hosted_invoice_url

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "amount_minor": amount_minor,
            }
        )
        if payment_status is not UNSET:
            field_dict["payment_status"] = payment_status
        if amount_remaining_minor is not UNSET:
            field_dict["amount_remaining_minor"] = amount_remaining_minor
        if due_at is not UNSET:
            field_dict["due_at"] = due_at
        if paid_at is not UNSET:
            field_dict["paid_at"] = paid_at
        if hosted_invoice_url is not UNSET:
            field_dict["hosted_invoice_url"] = hosted_invoice_url

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        amount_minor = d.pop("amount_minor")

        _payment_status = d.pop("payment_status", UNSET)
        payment_status: ConsumerBillingInvoicePaymentStatus | Unset
        if isinstance(_payment_status, Unset):
            payment_status = UNSET
        else:
            payment_status = check_consumer_billing_invoice_payment_status(_payment_status)

        amount_remaining_minor = d.pop("amount_remaining_minor", UNSET)

        _due_at = d.pop("due_at", UNSET)
        due_at: datetime.datetime | Unset
        if isinstance(_due_at, Unset):
            due_at = UNSET
        else:
            due_at = datetime.datetime.fromisoformat(_due_at)

        _paid_at = d.pop("paid_at", UNSET)
        paid_at: datetime.datetime | Unset
        if isinstance(_paid_at, Unset):
            paid_at = UNSET
        else:
            paid_at = datetime.datetime.fromisoformat(_paid_at)

        hosted_invoice_url = d.pop("hosted_invoice_url", UNSET)

        consumer_billing_invoice = cls(
            amount_minor=amount_minor,
            payment_status=payment_status,
            amount_remaining_minor=amount_remaining_minor,
            due_at=due_at,
            paid_at=paid_at,
            hosted_invoice_url=hosted_invoice_url,
        )

        consumer_billing_invoice.additional_properties = d
        return consumer_billing_invoice

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
