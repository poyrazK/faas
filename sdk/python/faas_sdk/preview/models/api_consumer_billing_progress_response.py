from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_consumer_billing_invoice_progress import APIConsumerBillingInvoiceProgress
    from ..models.api_consumer_billing_statement_progress import APIConsumerBillingStatementProgress
    from ..models.api_consumer_billing_usage_progress import APIConsumerBillingUsageProgress


T = TypeVar("T", bound="APIConsumerBillingProgressResponse")


@_attrs_define
class APIConsumerBillingProgressResponse:
    """Current onboarding milestones for one app consumer billing identity."""

    consumer_id: UUID
    registered_at: datetime.datetime
    usage: APIConsumerBillingUsageProgress
    """Summary of recent durable requests and priced usage through the last complete minute."""
    first_priced_at: datetime.datetime | Unset = UNSET
    """First minute with a positive priced charge in the customer's first 30 days."""
    statement: APIConsumerBillingStatementProgress | Unset = UNSET
    """Statement associated with the chosen invoice, or the most relevant statement to review."""
    invoice: APIConsumerBillingInvoiceProgress | Unset = UNSET
    """Provider invoice status as recorded by the account's payment sync."""
    first_live_paid_at: datetime.datetime | Unset = UNSET
    """Earliest recorded positive invoice payment in live mode for this customer."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        consumer_id = str(self.consumer_id)

        registered_at = self.registered_at.isoformat()

        usage = self.usage.to_dict()

        first_priced_at: str | Unset = UNSET
        if not isinstance(self.first_priced_at, Unset):
            first_priced_at = self.first_priced_at.isoformat()

        statement: dict[str, Any] | Unset = UNSET
        if not isinstance(self.statement, Unset):
            statement = self.statement.to_dict()

        invoice: dict[str, Any] | Unset = UNSET
        if not isinstance(self.invoice, Unset):
            invoice = self.invoice.to_dict()

        first_live_paid_at: str | Unset = UNSET
        if not isinstance(self.first_live_paid_at, Unset):
            first_live_paid_at = self.first_live_paid_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "consumer_id": consumer_id,
                "registered_at": registered_at,
                "usage": usage,
            }
        )
        if first_priced_at is not UNSET:
            field_dict["first_priced_at"] = first_priced_at
        if statement is not UNSET:
            field_dict["statement"] = statement
        if invoice is not UNSET:
            field_dict["invoice"] = invoice
        if first_live_paid_at is not UNSET:
            field_dict["first_live_paid_at"] = first_live_paid_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_consumer_billing_invoice_progress import APIConsumerBillingInvoiceProgress
        from ..models.api_consumer_billing_statement_progress import APIConsumerBillingStatementProgress
        from ..models.api_consumer_billing_usage_progress import APIConsumerBillingUsageProgress

        d = dict(src_dict)
        consumer_id = UUID(d.pop("consumer_id"))

        registered_at = datetime.datetime.fromisoformat(d.pop("registered_at"))

        usage = APIConsumerBillingUsageProgress.from_dict(d.pop("usage"))

        _first_priced_at = d.pop("first_priced_at", UNSET)
        first_priced_at: datetime.datetime | Unset
        if isinstance(_first_priced_at, Unset):
            first_priced_at = UNSET
        else:
            first_priced_at = datetime.datetime.fromisoformat(_first_priced_at)

        _statement = d.pop("statement", UNSET)
        statement: APIConsumerBillingStatementProgress | Unset
        if isinstance(_statement, Unset):
            statement = UNSET
        else:
            statement = APIConsumerBillingStatementProgress.from_dict(_statement)

        _invoice = d.pop("invoice", UNSET)
        invoice: APIConsumerBillingInvoiceProgress | Unset
        if isinstance(_invoice, Unset):
            invoice = UNSET
        else:
            invoice = APIConsumerBillingInvoiceProgress.from_dict(_invoice)

        _first_live_paid_at = d.pop("first_live_paid_at", UNSET)
        first_live_paid_at: datetime.datetime | Unset
        if isinstance(_first_live_paid_at, Unset):
            first_live_paid_at = UNSET
        else:
            first_live_paid_at = datetime.datetime.fromisoformat(_first_live_paid_at)

        api_consumer_billing_progress_response = cls(
            consumer_id=consumer_id,
            registered_at=registered_at,
            usage=usage,
            first_priced_at=first_priced_at,
            statement=statement,
            invoice=invoice,
            first_live_paid_at=first_live_paid_at,
        )

        api_consumer_billing_progress_response.additional_properties = d
        return api_consumer_billing_progress_response

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
