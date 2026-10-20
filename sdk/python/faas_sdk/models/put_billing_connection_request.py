from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.put_billing_connection_request_collection import (
    PutBillingConnectionRequestCollection,
    check_put_billing_connection_request_collection,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="PutBillingConnectionRequest")


@_attrs_define
class PutBillingConnectionRequest:
    """The account's own key for one billing provider (ADR-1502, ADR-1507)."""

    secret_key: str
    """A Stripe secret or restricted key (sk_… or rk_…), a Paddle API key (pdl_…), or a Lago API key."""
    api_url: str | Unset = UNSET
    """Lago only: the API origin for the EU region or a self-hosted Lago, https:// only; default
    https://api.getlago.com."""
    collection: PutBillingConnectionRequestCollection | Unset = "none"
    """What the provider does with exported invoices (ADR-1311): none leaves drafts (Lago issues them without
    charging); send has Stripe email the invoice; charge has Stripe charge the customer's default payment method, or
    Lago charge through its payment provider. Paddle supports none and send: send bills the transaction so Paddle
    emails the invoice to the customer's most recent address; Paddle cannot charge a saved card for an invoice."""
    days_until_due: int | Unset = 30
    """With collection send, days until a sent invoice is due."""
    automatic_tax: bool | Unset = False
    """Stripe only: have Stripe Tax calculate tax on exported invoices (ADR-1313). Needs Stripe Tax and customer
    addresses."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        secret_key = self.secret_key

        api_url = self.api_url

        collection: str | Unset = UNSET
        if not isinstance(self.collection, Unset):
            collection = self.collection

        days_until_due = self.days_until_due

        automatic_tax = self.automatic_tax

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "secret_key": secret_key,
            }
        )
        if api_url is not UNSET:
            field_dict["api_url"] = api_url
        if collection is not UNSET:
            field_dict["collection"] = collection
        if days_until_due is not UNSET:
            field_dict["days_until_due"] = days_until_due
        if automatic_tax is not UNSET:
            field_dict["automatic_tax"] = automatic_tax

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        secret_key = d.pop("secret_key")

        api_url = d.pop("api_url", UNSET)

        _collection = d.pop("collection", UNSET)
        collection: PutBillingConnectionRequestCollection | Unset
        if isinstance(_collection, Unset):
            collection = UNSET
        else:
            collection = check_put_billing_connection_request_collection(_collection)

        days_until_due = d.pop("days_until_due", UNSET)

        automatic_tax = d.pop("automatic_tax", UNSET)

        put_billing_connection_request = cls(
            secret_key=secret_key,
            api_url=api_url,
            collection=collection,
            days_until_due=days_until_due,
            automatic_tax=automatic_tax,
        )

        put_billing_connection_request.additional_properties = d
        return put_billing_connection_request

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
