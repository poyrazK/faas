from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ApproveBillingRunDraftExportRequest")


@_attrs_define
class ApproveBillingRunDraftExportRequest:
    """The exact Stripe draft and its amount due as shown in the review."""

    consumer_id: UUID
    statement_id: UUID
    revision: int
    currency: str
    amount_due_millicents: int
    stripe_customer: str
    livemode: bool
    connection_updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        consumer_id = str(self.consumer_id)

        statement_id = str(self.statement_id)

        revision = self.revision

        currency = self.currency

        amount_due_millicents = self.amount_due_millicents

        stripe_customer = self.stripe_customer

        livemode = self.livemode

        connection_updated_at = self.connection_updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "consumer_id": consumer_id,
                "statement_id": statement_id,
                "revision": revision,
                "currency": currency,
                "amount_due_millicents": amount_due_millicents,
                "stripe_customer": stripe_customer,
                "livemode": livemode,
                "connection_updated_at": connection_updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        consumer_id = UUID(d.pop("consumer_id"))

        statement_id = UUID(d.pop("statement_id"))

        revision = d.pop("revision")

        currency = d.pop("currency")

        amount_due_millicents = d.pop("amount_due_millicents")

        stripe_customer = d.pop("stripe_customer")

        livemode = d.pop("livemode")

        connection_updated_at = datetime.datetime.fromisoformat(d.pop("connection_updated_at"))

        approve_billing_run_draft_export_request = cls(
            consumer_id=consumer_id,
            statement_id=statement_id,
            revision=revision,
            currency=currency,
            amount_due_millicents=amount_due_millicents,
            stripe_customer=stripe_customer,
            livemode=livemode,
            connection_updated_at=connection_updated_at,
        )

        approve_billing_run_draft_export_request.additional_properties = d
        return approve_billing_run_draft_export_request

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
