from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="LinkBillingRunStripeCustomerRequest")


@_attrs_define
class LinkBillingRunStripeCustomerRequest:
    """The Stripe customer and original draft review being linked."""

    stripe_customer: str
    statement_id: UUID
    livemode: bool
    connection_updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        stripe_customer = self.stripe_customer

        statement_id = str(self.statement_id)

        livemode = self.livemode

        connection_updated_at = self.connection_updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "stripe_customer": stripe_customer,
                "statement_id": statement_id,
                "livemode": livemode,
                "connection_updated_at": connection_updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        stripe_customer = d.pop("stripe_customer")

        statement_id = UUID(d.pop("statement_id"))

        livemode = d.pop("livemode")

        connection_updated_at = datetime.datetime.fromisoformat(d.pop("connection_updated_at"))

        link_billing_run_stripe_customer_request = cls(
            stripe_customer=stripe_customer,
            statement_id=statement_id,
            livemode=livemode,
            connection_updated_at=connection_updated_at,
        )

        link_billing_run_stripe_customer_request.additional_properties = d
        return link_billing_run_stripe_customer_request

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
