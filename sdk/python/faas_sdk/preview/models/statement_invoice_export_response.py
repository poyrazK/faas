from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.billing_provider import BillingProvider, check_billing_provider
from ..models.statement_invoice_export_response_collection import (
    StatementInvoiceExportResponseCollection,
    check_statement_invoice_export_response_collection,
)
from ..models.statement_invoice_export_response_payment_status import (
    StatementInvoiceExportResponsePaymentStatus,
    check_statement_invoice_export_response_payment_status,
)
from ..models.statement_invoice_export_response_source_kind import (
    StatementInvoiceExportResponseSourceKind,
    check_statement_invoice_export_response_source_kind,
)
from ..models.statement_invoice_export_response_status import (
    StatementInvoiceExportResponseStatus,
    check_statement_invoice_export_response_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="StatementInvoiceExportResponse")


@_attrs_define
class StatementInvoiceExportResponse:
    """A finalized statement's invoice in the account's billing provider."""

    statement_id: UUID
    provider: BillingProvider
    """A billing provider statements export to."""
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
    collection: StatementInvoiceExportResponseCollection | Unset = UNSET
    """What the provider was asked to do with the invoice when the export began."""
    automatic_tax: bool | Unset = UNSET
    """Whether Stripe Tax calculated this invoice's tax."""
    completed_at: datetime.datetime | Unset = UNSET
    source_kind: StatementInvoiceExportResponseSourceKind | Unset = UNSET
    payment_status: StatementInvoiceExportResponsePaymentStatus | Unset = UNSET
    """The provider invoice's status as last read by Gregale; deleted means the provider no longer has it. Absent
    until the first sync."""
    amount_paid_minor: int | Unset = UNSET
    amount_remaining_minor: int | Unset = UNSET
    due_at: datetime.datetime | Unset = UNSET
    paid_at: datetime.datetime | Unset = UNSET
    hosted_invoice_url: str | Unset = UNSET
    """The provider's customer-facing invoice or checkout page once there is one."""
    overdue: bool | Unset = UNSET
    """Open, past its due date, with an amount remaining."""
    status_synced_at: datetime.datetime | Unset = UNSET
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

        collection: str | Unset = UNSET
        if not isinstance(self.collection, Unset):
            collection = self.collection

        automatic_tax = self.automatic_tax

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        source_kind: str | Unset = UNSET
        if not isinstance(self.source_kind, Unset):
            source_kind = self.source_kind

        payment_status: str | Unset = UNSET
        if not isinstance(self.payment_status, Unset):
            payment_status = self.payment_status

        amount_paid_minor = self.amount_paid_minor

        amount_remaining_minor = self.amount_remaining_minor

        due_at: str | Unset = UNSET
        if not isinstance(self.due_at, Unset):
            due_at = self.due_at.isoformat()

        paid_at: str | Unset = UNSET
        if not isinstance(self.paid_at, Unset):
            paid_at = self.paid_at.isoformat()

        hosted_invoice_url = self.hosted_invoice_url

        overdue = self.overdue

        status_synced_at: str | Unset = UNSET
        if not isinstance(self.status_synced_at, Unset):
            status_synced_at = self.status_synced_at.isoformat()

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
        if collection is not UNSET:
            field_dict["collection"] = collection
        if automatic_tax is not UNSET:
            field_dict["automatic_tax"] = automatic_tax
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at
        if source_kind is not UNSET:
            field_dict["source_kind"] = source_kind
        if payment_status is not UNSET:
            field_dict["payment_status"] = payment_status
        if amount_paid_minor is not UNSET:
            field_dict["amount_paid_minor"] = amount_paid_minor
        if amount_remaining_minor is not UNSET:
            field_dict["amount_remaining_minor"] = amount_remaining_minor
        if due_at is not UNSET:
            field_dict["due_at"] = due_at
        if paid_at is not UNSET:
            field_dict["paid_at"] = paid_at
        if hosted_invoice_url is not UNSET:
            field_dict["hosted_invoice_url"] = hosted_invoice_url
        if overdue is not UNSET:
            field_dict["overdue"] = overdue
        if status_synced_at is not UNSET:
            field_dict["status_synced_at"] = status_synced_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        statement_id = UUID(d.pop("statement_id"))

        provider = check_billing_provider(d.pop("provider"))

        customer = d.pop("customer")

        status = check_statement_invoice_export_response_status(d.pop("status"))

        currency = d.pop("currency")

        amount_minor = d.pop("amount_minor")

        livemode = d.pop("livemode")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        invoice_id = d.pop("invoice_id", UNSET)

        dashboard_url = d.pop("dashboard_url", UNSET)

        _collection = d.pop("collection", UNSET)
        collection: StatementInvoiceExportResponseCollection | Unset
        if isinstance(_collection, Unset):
            collection = UNSET
        else:
            collection = check_statement_invoice_export_response_collection(_collection)

        automatic_tax = d.pop("automatic_tax", UNSET)

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        _source_kind = d.pop("source_kind", UNSET)
        source_kind: StatementInvoiceExportResponseSourceKind | Unset
        if isinstance(_source_kind, Unset):
            source_kind = UNSET
        else:
            source_kind = check_statement_invoice_export_response_source_kind(_source_kind)

        _payment_status = d.pop("payment_status", UNSET)
        payment_status: StatementInvoiceExportResponsePaymentStatus | Unset
        if isinstance(_payment_status, Unset):
            payment_status = UNSET
        else:
            payment_status = check_statement_invoice_export_response_payment_status(_payment_status)

        amount_paid_minor = d.pop("amount_paid_minor", UNSET)

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

        overdue = d.pop("overdue", UNSET)

        _status_synced_at = d.pop("status_synced_at", UNSET)
        status_synced_at: datetime.datetime | Unset
        if isinstance(_status_synced_at, Unset):
            status_synced_at = UNSET
        else:
            status_synced_at = datetime.datetime.fromisoformat(_status_synced_at)

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
            collection=collection,
            automatic_tax=automatic_tax,
            completed_at=completed_at,
            source_kind=source_kind,
            payment_status=payment_status,
            amount_paid_minor=amount_paid_minor,
            amount_remaining_minor=amount_remaining_minor,
            due_at=due_at,
            paid_at=paid_at,
            hosted_invoice_url=hosted_invoice_url,
            overdue=overdue,
            status_synced_at=status_synced_at,
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
