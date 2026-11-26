from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.billing_run_draft_export_row_blocker_code import (
    BillingRunDraftExportRowBlockerCode,
    check_billing_run_draft_export_row_blocker_code,
)
from ..models.billing_run_draft_export_row_status import (
    BillingRunDraftExportRowStatus,
    check_billing_run_draft_export_row_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.statement_invoice_export_response import StatementInvoiceExportResponse


T = TypeVar("T", bound="BillingRunDraftExportRow")


@_attrs_define
class BillingRunDraftExportRow:
    """Live review of one billing run item for Stripe draft export."""

    id: str
    customer_name: str
    status: BillingRunDraftExportRowStatus
    consumer_id: UUID | Unset = UNSET
    stripe_customer: str | Unset = UNSET
    statement_id: UUID | Unset = UNSET
    revision: int | Unset = UNSET
    currency: str | Unset = UNSET
    amount_due_millicents: int | Unset = UNSET
    """Projected amount due after credit, before provider tax and rounding."""
    reason: str | Unset = UNSET
    blocker_code: BillingRunDraftExportRowBlockerCode | Unset = UNSET
    invoice: StatementInvoiceExportResponse | Unset = UNSET
    """A finalized statement's invoice in the account's billing provider."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        customer_name = self.customer_name

        status: str = self.status

        consumer_id: str | Unset = UNSET
        if not isinstance(self.consumer_id, Unset):
            consumer_id = str(self.consumer_id)

        stripe_customer = self.stripe_customer

        statement_id: str | Unset = UNSET
        if not isinstance(self.statement_id, Unset):
            statement_id = str(self.statement_id)

        revision = self.revision

        currency = self.currency

        amount_due_millicents = self.amount_due_millicents

        reason = self.reason

        blocker_code: str | Unset = UNSET
        if not isinstance(self.blocker_code, Unset):
            blocker_code = self.blocker_code

        invoice: dict[str, Any] | Unset = UNSET
        if not isinstance(self.invoice, Unset):
            invoice = self.invoice.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "customer_name": customer_name,
                "status": status,
            }
        )
        if consumer_id is not UNSET:
            field_dict["consumer_id"] = consumer_id
        if stripe_customer is not UNSET:
            field_dict["stripe_customer"] = stripe_customer
        if statement_id is not UNSET:
            field_dict["statement_id"] = statement_id
        if revision is not UNSET:
            field_dict["revision"] = revision
        if currency is not UNSET:
            field_dict["currency"] = currency
        if amount_due_millicents is not UNSET:
            field_dict["amount_due_millicents"] = amount_due_millicents
        if reason is not UNSET:
            field_dict["reason"] = reason
        if blocker_code is not UNSET:
            field_dict["blocker_code"] = blocker_code
        if invoice is not UNSET:
            field_dict["invoice"] = invoice

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.statement_invoice_export_response import StatementInvoiceExportResponse

        d = dict(src_dict)
        id = d.pop("id")

        customer_name = d.pop("customer_name")

        status = check_billing_run_draft_export_row_status(d.pop("status"))

        _consumer_id = d.pop("consumer_id", UNSET)
        consumer_id: UUID | Unset
        if isinstance(_consumer_id, Unset):
            consumer_id = UNSET
        else:
            consumer_id = UUID(_consumer_id)

        stripe_customer = d.pop("stripe_customer", UNSET)

        _statement_id = d.pop("statement_id", UNSET)
        statement_id: UUID | Unset
        if isinstance(_statement_id, Unset):
            statement_id = UNSET
        else:
            statement_id = UUID(_statement_id)

        revision = d.pop("revision", UNSET)

        currency = d.pop("currency", UNSET)

        amount_due_millicents = d.pop("amount_due_millicents", UNSET)

        reason = d.pop("reason", UNSET)

        _blocker_code = d.pop("blocker_code", UNSET)
        blocker_code: BillingRunDraftExportRowBlockerCode | Unset
        if isinstance(_blocker_code, Unset):
            blocker_code = UNSET
        else:
            blocker_code = check_billing_run_draft_export_row_blocker_code(_blocker_code)

        _invoice = d.pop("invoice", UNSET)
        invoice: StatementInvoiceExportResponse | Unset
        if isinstance(_invoice, Unset):
            invoice = UNSET
        else:
            invoice = StatementInvoiceExportResponse.from_dict(_invoice)

        billing_run_draft_export_row = cls(
            id=id,
            customer_name=customer_name,
            status=status,
            consumer_id=consumer_id,
            stripe_customer=stripe_customer,
            statement_id=statement_id,
            revision=revision,
            currency=currency,
            amount_due_millicents=amount_due_millicents,
            reason=reason,
            blocker_code=blocker_code,
            invoice=invoice,
        )

        billing_run_draft_export_row.additional_properties = d
        return billing_run_draft_export_row

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
