from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.billing_run_item_response_completeness import (
    BillingRunItemResponseCompleteness,
    check_billing_run_item_response_completeness,
)
from ..models.billing_run_item_response_outcome import (
    BillingRunItemResponseOutcome,
    check_billing_run_item_response_outcome,
)
from ..models.billing_run_item_response_payment_status import (
    BillingRunItemResponsePaymentStatus,
    check_billing_run_item_response_payment_status,
)
from ..models.billing_run_item_response_source_kind import (
    BillingRunItemResponseSourceKind,
    check_billing_run_item_response_source_kind,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="BillingRunItemResponse")


@_attrs_define
class BillingRunItemResponse:
    """What a billing run did for one app consumer, platform tenant or Product Control customer. A product_control item
    names its app in app_id and its customer in platform_tenant_id; its statement_id is the Product Control statement
    document.

    """

    source_kind: BillingRunItemResponseSourceKind
    external_ref: str
    """The consumer's or tenant's external_ref when the run started."""
    outcome: BillingRunItemResponseOutcome
    """up_to_date: nothing new to bill or export. held: needs attention, such as unpriced usage, a completeness
    gap, or Product Control runs with no recorded outcome. skipped: billed through a platform tenant, or already
    invoiced through the other path or by hand."""
    exported_invoices: int
    """Provider invoices this run created for the customer."""
    attempts: int
    updated_at: datetime.datetime
    app_id: UUID | Unset = UNSET
    consumer_id: UUID | Unset = UNSET
    platform_tenant_id: UUID | Unset = UNSET
    statement_id: UUID | Unset = UNSET
    revision: int | Unset = UNSET
    currency: str | Unset = UNSET
    amount_millicents: int | Unset = UNSET
    """The statement revision's amount in millicents."""
    completeness: BillingRunItemResponseCompleteness | Unset = UNSET
    invoice_id: str | Unset = UNSET
    """The last provider invoice this run created for the customer."""
    reason: str | Unset = UNSET
    """Why the customer was held, skipped or failed, or why a retry is pending."""
    payment_status: BillingRunItemResponsePaymentStatus | Unset = UNSET
    """Payment status of the invoice exported for the item's statement (ADR-1504)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source_kind: str = self.source_kind

        external_ref = self.external_ref

        outcome: str = self.outcome

        exported_invoices = self.exported_invoices

        attempts = self.attempts

        updated_at = self.updated_at.isoformat()

        app_id: str | Unset = UNSET
        if not isinstance(self.app_id, Unset):
            app_id = str(self.app_id)

        consumer_id: str | Unset = UNSET
        if not isinstance(self.consumer_id, Unset):
            consumer_id = str(self.consumer_id)

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        statement_id: str | Unset = UNSET
        if not isinstance(self.statement_id, Unset):
            statement_id = str(self.statement_id)

        revision = self.revision

        currency = self.currency

        amount_millicents = self.amount_millicents

        completeness: str | Unset = UNSET
        if not isinstance(self.completeness, Unset):
            completeness = self.completeness

        invoice_id = self.invoice_id

        reason = self.reason

        payment_status: str | Unset = UNSET
        if not isinstance(self.payment_status, Unset):
            payment_status = self.payment_status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source_kind": source_kind,
                "external_ref": external_ref,
                "outcome": outcome,
                "exported_invoices": exported_invoices,
                "attempts": attempts,
                "updated_at": updated_at,
            }
        )
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if consumer_id is not UNSET:
            field_dict["consumer_id"] = consumer_id
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id
        if statement_id is not UNSET:
            field_dict["statement_id"] = statement_id
        if revision is not UNSET:
            field_dict["revision"] = revision
        if currency is not UNSET:
            field_dict["currency"] = currency
        if amount_millicents is not UNSET:
            field_dict["amount_millicents"] = amount_millicents
        if completeness is not UNSET:
            field_dict["completeness"] = completeness
        if invoice_id is not UNSET:
            field_dict["invoice_id"] = invoice_id
        if reason is not UNSET:
            field_dict["reason"] = reason
        if payment_status is not UNSET:
            field_dict["payment_status"] = payment_status

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        source_kind = check_billing_run_item_response_source_kind(d.pop("source_kind"))

        external_ref = d.pop("external_ref")

        outcome = check_billing_run_item_response_outcome(d.pop("outcome"))

        exported_invoices = d.pop("exported_invoices")

        attempts = d.pop("attempts")

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _app_id = d.pop("app_id", UNSET)
        app_id: UUID | Unset
        if isinstance(_app_id, Unset):
            app_id = UNSET
        else:
            app_id = UUID(_app_id)

        _consumer_id = d.pop("consumer_id", UNSET)
        consumer_id: UUID | Unset
        if isinstance(_consumer_id, Unset):
            consumer_id = UNSET
        else:
            consumer_id = UUID(_consumer_id)

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        _statement_id = d.pop("statement_id", UNSET)
        statement_id: UUID | Unset
        if isinstance(_statement_id, Unset):
            statement_id = UNSET
        else:
            statement_id = UUID(_statement_id)

        revision = d.pop("revision", UNSET)

        currency = d.pop("currency", UNSET)

        amount_millicents = d.pop("amount_millicents", UNSET)

        _completeness = d.pop("completeness", UNSET)
        completeness: BillingRunItemResponseCompleteness | Unset
        if isinstance(_completeness, Unset):
            completeness = UNSET
        else:
            completeness = check_billing_run_item_response_completeness(_completeness)

        invoice_id = d.pop("invoice_id", UNSET)

        reason = d.pop("reason", UNSET)

        _payment_status = d.pop("payment_status", UNSET)
        payment_status: BillingRunItemResponsePaymentStatus | Unset
        if isinstance(_payment_status, Unset):
            payment_status = UNSET
        else:
            payment_status = check_billing_run_item_response_payment_status(_payment_status)

        billing_run_item_response = cls(
            source_kind=source_kind,
            external_ref=external_ref,
            outcome=outcome,
            exported_invoices=exported_invoices,
            attempts=attempts,
            updated_at=updated_at,
            app_id=app_id,
            consumer_id=consumer_id,
            platform_tenant_id=platform_tenant_id,
            statement_id=statement_id,
            revision=revision,
            currency=currency,
            amount_millicents=amount_millicents,
            completeness=completeness,
            invoice_id=invoice_id,
            reason=reason,
            payment_status=payment_status,
        )

        billing_run_item_response.additional_properties = d
        return billing_run_item_response

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
