from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.api_second_invoice_review import APISecondInvoiceReview


T = TypeVar("T", bound="APIMonetizationFunnelResponse")


@_attrs_define
class APIMonetizationFunnelResponse:
    """Usage, invoicing, and payment milestones for a recent app consumer registration cohort."""

    since: datetime.datetime
    """Inclusive registration cohort start in UTC."""
    until: datetime.datetime
    """Exclusive registration cohort end in UTC."""
    total_registered: int
    """All registrations in the cohort before applying the sample limit."""
    registered: int
    """Registrations included in the milestone counts."""
    priced_in_first_30_days: int
    """Customers with positively priced usage during their first 30 days."""
    live_invoice_sent: int
    """Customers with a live Stripe invoice send recorded by Gregale."""
    live_payment: int
    """Customers with a recorded positive live Stripe invoice payment."""
    next_billable_month: int
    """Paying customers with a later non-overlapping billing month that has a finalized positive statement balance."""
    next_month_live_payment: int
    """Customers with another positive live payment in their next billable UTC month."""
    second_invoice_review: list[APISecondInvoiceReview]
    """One next-cycle review row per customer with a first live payment and a later billable statement or
    actionable draft."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        since = self.since.isoformat()

        until = self.until.isoformat()

        total_registered = self.total_registered

        registered = self.registered

        priced_in_first_30_days = self.priced_in_first_30_days

        live_invoice_sent = self.live_invoice_sent

        live_payment = self.live_payment

        next_billable_month = self.next_billable_month

        next_month_live_payment = self.next_month_live_payment

        second_invoice_review = []
        for second_invoice_review_item_data in self.second_invoice_review:
            second_invoice_review_item = second_invoice_review_item_data.to_dict()
            second_invoice_review.append(second_invoice_review_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "since": since,
                "until": until,
                "total_registered": total_registered,
                "registered": registered,
                "priced_in_first_30_days": priced_in_first_30_days,
                "live_invoice_sent": live_invoice_sent,
                "live_payment": live_payment,
                "next_billable_month": next_billable_month,
                "next_month_live_payment": next_month_live_payment,
                "second_invoice_review": second_invoice_review,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_second_invoice_review import APISecondInvoiceReview

        d = dict(src_dict)
        since = datetime.datetime.fromisoformat(d.pop("since"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        total_registered = d.pop("total_registered")

        registered = d.pop("registered")

        priced_in_first_30_days = d.pop("priced_in_first_30_days")

        live_invoice_sent = d.pop("live_invoice_sent")

        live_payment = d.pop("live_payment")

        next_billable_month = d.pop("next_billable_month")

        next_month_live_payment = d.pop("next_month_live_payment")

        second_invoice_review = []
        _second_invoice_review = d.pop("second_invoice_review")
        for second_invoice_review_item_data in _second_invoice_review:
            second_invoice_review_item = APISecondInvoiceReview.from_dict(second_invoice_review_item_data)

            second_invoice_review.append(second_invoice_review_item)

        api_monetization_funnel_response = cls(
            since=since,
            until=until,
            total_registered=total_registered,
            registered=registered,
            priced_in_first_30_days=priced_in_first_30_days,
            live_invoice_sent=live_invoice_sent,
            live_payment=live_payment,
            next_billable_month=next_billable_month,
            next_month_live_payment=next_month_live_payment,
            second_invoice_review=second_invoice_review,
        )

        api_monetization_funnel_response.additional_properties = d
        return api_monetization_funnel_response

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
