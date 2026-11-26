from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="BillingRunCollectionTotal")


@_attrs_define
class BillingRunCollectionTotal:
    """Recorded Stripe payments and balances for one currency and mode in the run."""

    currency: str
    livemode: bool
    amount_paid_minor: int
    amount_outstanding_minor: int
    amount_overdue_minor: int
    awaiting_check: int
    """Issued or sent invoices with an unconfirmed balance."""
    invoices_due: int
    """Invoices with a positive recorded payment or balance whose due date has passed."""
    paid_by_due_date: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        currency = self.currency

        livemode = self.livemode

        amount_paid_minor = self.amount_paid_minor

        amount_outstanding_minor = self.amount_outstanding_minor

        amount_overdue_minor = self.amount_overdue_minor

        awaiting_check = self.awaiting_check

        invoices_due = self.invoices_due

        paid_by_due_date = self.paid_by_due_date

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "currency": currency,
                "livemode": livemode,
                "amount_paid_minor": amount_paid_minor,
                "amount_outstanding_minor": amount_outstanding_minor,
                "amount_overdue_minor": amount_overdue_minor,
                "awaiting_check": awaiting_check,
                "invoices_due": invoices_due,
                "paid_by_due_date": paid_by_due_date,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        currency = d.pop("currency")

        livemode = d.pop("livemode")

        amount_paid_minor = d.pop("amount_paid_minor")

        amount_outstanding_minor = d.pop("amount_outstanding_minor")

        amount_overdue_minor = d.pop("amount_overdue_minor")

        awaiting_check = d.pop("awaiting_check")

        invoices_due = d.pop("invoices_due")

        paid_by_due_date = d.pop("paid_by_due_date")

        billing_run_collection_total = cls(
            currency=currency,
            livemode=livemode,
            amount_paid_minor=amount_paid_minor,
            amount_outstanding_minor=amount_outstanding_minor,
            amount_overdue_minor=amount_overdue_minor,
            awaiting_check=awaiting_check,
            invoices_due=invoices_due,
            paid_by_due_date=paid_by_due_date,
        )

        billing_run_collection_total.additional_properties = d
        return billing_run_collection_total

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
