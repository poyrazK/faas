from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="StripeInvoiceSendReview")


@_attrs_define
class StripeInvoiceSendReview:
    """Provider-confirmed terms shown for owner approval before manual Stripe delivery."""

    invoice_id: str
    stripe_customer: str
    recipient_email: str
    currency: str
    amount_due_minor: int
    due_at: datetime.datetime
    livemode: bool
    review_token: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        invoice_id = self.invoice_id

        stripe_customer = self.stripe_customer

        recipient_email = self.recipient_email

        currency = self.currency

        amount_due_minor = self.amount_due_minor

        due_at = self.due_at.isoformat()

        livemode = self.livemode

        review_token = self.review_token

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "invoice_id": invoice_id,
                "stripe_customer": stripe_customer,
                "recipient_email": recipient_email,
                "currency": currency,
                "amount_due_minor": amount_due_minor,
                "due_at": due_at,
                "livemode": livemode,
                "review_token": review_token,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        invoice_id = d.pop("invoice_id")

        stripe_customer = d.pop("stripe_customer")

        recipient_email = d.pop("recipient_email")

        currency = d.pop("currency")

        amount_due_minor = d.pop("amount_due_minor")

        due_at = datetime.datetime.fromisoformat(d.pop("due_at"))

        livemode = d.pop("livemode")

        review_token = d.pop("review_token")

        stripe_invoice_send_review = cls(
            invoice_id=invoice_id,
            stripe_customer=stripe_customer,
            recipient_email=recipient_email,
            currency=currency,
            amount_due_minor=amount_due_minor,
            due_at=due_at,
            livemode=livemode,
            review_token=review_token,
        )

        stripe_invoice_send_review.additional_properties = d
        return stripe_invoice_send_review

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
