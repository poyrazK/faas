from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.api_consumer_billing_invoice_progress_payment_status import (
    APIConsumerBillingInvoiceProgressPaymentStatus,
    check_api_consumer_billing_invoice_progress_payment_status,
)
from ..models.billing_provider import BillingProvider, check_billing_provider
from ..types import UNSET, Unset

T = TypeVar("T", bound="APIConsumerBillingInvoiceProgress")


@_attrs_define
class APIConsumerBillingInvoiceProgress:
    """Provider invoice status as recorded by the account's payment sync."""

    id: str
    provider: BillingProvider
    """A billing provider statements export to."""
    livemode: bool
    payment_status: APIConsumerBillingInvoiceProgressPaymentStatus | Unset = UNSET
    sent_at: datetime.datetime | Unset = UNSET
    paid_at: datetime.datetime | Unset = UNSET
    """Provider timestamp for a confirmed positive payment on this invoice."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        provider: str = self.provider

        livemode = self.livemode

        payment_status: str | Unset = UNSET
        if not isinstance(self.payment_status, Unset):
            payment_status = self.payment_status

        sent_at: str | Unset = UNSET
        if not isinstance(self.sent_at, Unset):
            sent_at = self.sent_at.isoformat()

        paid_at: str | Unset = UNSET
        if not isinstance(self.paid_at, Unset):
            paid_at = self.paid_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "provider": provider,
                "livemode": livemode,
            }
        )
        if payment_status is not UNSET:
            field_dict["payment_status"] = payment_status
        if sent_at is not UNSET:
            field_dict["sent_at"] = sent_at
        if paid_at is not UNSET:
            field_dict["paid_at"] = paid_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        provider = check_billing_provider(d.pop("provider"))

        livemode = d.pop("livemode")

        _payment_status = d.pop("payment_status", UNSET)
        payment_status: APIConsumerBillingInvoiceProgressPaymentStatus | Unset
        if isinstance(_payment_status, Unset):
            payment_status = UNSET
        else:
            payment_status = check_api_consumer_billing_invoice_progress_payment_status(_payment_status)

        _sent_at = d.pop("sent_at", UNSET)
        sent_at: datetime.datetime | Unset
        if isinstance(_sent_at, Unset):
            sent_at = UNSET
        else:
            sent_at = datetime.datetime.fromisoformat(_sent_at)

        _paid_at = d.pop("paid_at", UNSET)
        paid_at: datetime.datetime | Unset
        if isinstance(_paid_at, Unset):
            paid_at = UNSET
        else:
            paid_at = datetime.datetime.fromisoformat(_paid_at)

        api_consumer_billing_invoice_progress = cls(
            id=id,
            provider=provider,
            livemode=livemode,
            payment_status=payment_status,
            sent_at=sent_at,
            paid_at=paid_at,
        )

        api_consumer_billing_invoice_progress.additional_properties = d
        return api_consumer_billing_invoice_progress

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
