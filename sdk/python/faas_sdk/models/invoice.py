from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.invoice_currency import InvoiceCurrency, check_invoice_currency
from ..models.invoice_provider import InvoiceProvider, check_invoice_provider
from ..models.invoice_status import InvoiceStatus, check_invoice_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="Invoice")


@_attrs_define
class Invoice:
    """One persisted billing-provider invoice (issue #259). Money is
    integer cents in the provider's currency; EUR is the platform
    currency today, the field is preserved per-row so multi-currency
    support can land without a backfill. The PDF availability flag
    is the only PDF surface we expose — the hosted PDF URL is
    provider-scoped and the customer fetches it from the
    Stripe/Paddle portal, not via this API. The hosted URL itself
    is not on the wire; the column exists in invoices.hosted_url
    for PR-B audit only.

    """

    id: UUID
    provider: InvoiceProvider
    provider_invoice_id: str
    status: InvoiceStatus
    period_start: datetime.datetime
    period_end: datetime.datetime
    subtotal_cents: int
    tax_cents: int
    total_cents: int
    amount_paid_cents: int
    currency: InvoiceCurrency
    pdf_available: bool
    created_at: datetime.datetime
    number: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        provider: str = self.provider

        provider_invoice_id = self.provider_invoice_id

        status: str = self.status

        period_start = self.period_start.isoformat()

        period_end = self.period_end.isoformat()

        subtotal_cents = self.subtotal_cents

        tax_cents = self.tax_cents

        total_cents = self.total_cents

        amount_paid_cents = self.amount_paid_cents

        currency: str = self.currency

        pdf_available = self.pdf_available

        created_at = self.created_at.isoformat()

        number = self.number

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "provider": provider,
                "provider_invoice_id": provider_invoice_id,
                "status": status,
                "period_start": period_start,
                "period_end": period_end,
                "subtotal_cents": subtotal_cents,
                "tax_cents": tax_cents,
                "total_cents": total_cents,
                "amount_paid_cents": amount_paid_cents,
                "currency": currency,
                "pdf_available": pdf_available,
                "created_at": created_at,
            }
        )
        if number is not UNSET:
            field_dict["number"] = number

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        provider = check_invoice_provider(d.pop("provider"))

        provider_invoice_id = d.pop("provider_invoice_id")

        status = check_invoice_status(d.pop("status"))

        period_start = datetime.datetime.fromisoformat(d.pop("period_start"))

        period_end = datetime.datetime.fromisoformat(d.pop("period_end"))

        subtotal_cents = d.pop("subtotal_cents")

        tax_cents = d.pop("tax_cents")

        total_cents = d.pop("total_cents")

        amount_paid_cents = d.pop("amount_paid_cents")

        currency = check_invoice_currency(d.pop("currency"))

        pdf_available = d.pop("pdf_available")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        number = d.pop("number", UNSET)

        invoice = cls(
            id=id,
            provider=provider,
            provider_invoice_id=provider_invoice_id,
            status=status,
            period_start=period_start,
            period_end=period_end,
            subtotal_cents=subtotal_cents,
            tax_cents=tax_cents,
            total_cents=total_cents,
            amount_paid_cents=amount_paid_cents,
            currency=currency,
            pdf_available=pdf_available,
            created_at=created_at,
            number=number,
        )

        invoice.additional_properties = d
        return invoice

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
