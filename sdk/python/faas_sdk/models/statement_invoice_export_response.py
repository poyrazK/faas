from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.statement_invoice_export_response_provider import (
    StatementInvoiceExportResponseProvider,
    check_statement_invoice_export_response_provider,
)
from ..models.statement_invoice_export_response_status import (
    StatementInvoiceExportResponseStatus,
    check_statement_invoice_export_response_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="StatementInvoiceExportResponse")


@_attrs_define
class StatementInvoiceExportResponse:
    """A finalized statement's draft invoice in the account's Stripe (ADR-1300)."""

    statement_id: UUID
    provider: StatementInvoiceExportResponseProvider
    customer: str
    status: StatementInvoiceExportResponseStatus
    """creating means an earlier attempt stopped part-way; repeat the export to resume it."""
    currency: str
    amount_minor: int
    """Invoice total in the currency's minor unit (cents): the statement total rounded to the cent."""
    livemode: bool
    created_at: datetime.datetime
    invoice_id: str | Unset = UNSET
    dashboard_url: str | Unset = UNSET
    completed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        statement_id = str(self.statement_id)

        provider: str = self.provider

        customer = self.customer

        status: str = self.status

        currency = self.currency

        amount_minor = self.amount_minor

        livemode = self.livemode

        created_at = self.created_at.isoformat()

        invoice_id = self.invoice_id

        dashboard_url = self.dashboard_url

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "statement_id": statement_id,
                "provider": provider,
                "customer": customer,
                "status": status,
                "currency": currency,
                "amount_minor": amount_minor,
                "livemode": livemode,
                "created_at": created_at,
            }
        )
        if invoice_id is not UNSET:
            field_dict["invoice_id"] = invoice_id
        if dashboard_url is not UNSET:
            field_dict["dashboard_url"] = dashboard_url
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        statement_id = UUID(d.pop("statement_id"))

        provider = check_statement_invoice_export_response_provider(d.pop("provider"))

        customer = d.pop("customer")

        status = check_statement_invoice_export_response_status(d.pop("status"))

        currency = d.pop("currency")

        amount_minor = d.pop("amount_minor")

        livemode = d.pop("livemode")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        invoice_id = d.pop("invoice_id", UNSET)

        dashboard_url = d.pop("dashboard_url", UNSET)

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        statement_invoice_export_response = cls(
            statement_id=statement_id,
            provider=provider,
            customer=customer,
            status=status,
            currency=currency,
            amount_minor=amount_minor,
            livemode=livemode,
            created_at=created_at,
            invoice_id=invoice_id,
            dashboard_url=dashboard_url,
            completed_at=completed_at,
        )

        statement_invoice_export_response.additional_properties = d
        return statement_invoice_export_response

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
