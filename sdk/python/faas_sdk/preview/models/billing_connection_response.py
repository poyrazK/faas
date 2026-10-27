from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.billing_connection_response_collection import (
    BillingConnectionResponseCollection,
    check_billing_connection_response_collection,
)
from ..models.billing_provider import BillingProvider, check_billing_provider
from ..types import UNSET, Unset

T = TypeVar("T", bound="BillingConnectionResponse")


@_attrs_define
class BillingConnectionResponse:
    """A stored payment-provider key, identified without revealing it."""

    provider: BillingProvider
    """A billing provider statements export to."""
    key_last4: str
    livemode: bool
    """False for Stripe test and Paddle sandbox keys; Lago has no test mode."""
    created_at: datetime.datetime
    updated_at: datetime.datetime
    api_url: str | Unset = UNSET
    """The Lago API origin."""
    collection: BillingConnectionResponseCollection | Unset = UNSET
    """What the provider does with exported invoices."""
    days_until_due: int | Unset = UNSET
    """Days until a sent invoice is due."""
    automatic_tax: bool | Unset = UNSET
    """Whether Stripe Tax calculates tax on the connection's exports."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        provider: str = self.provider

        key_last4 = self.key_last4

        livemode = self.livemode

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

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
                "provider": provider,
                "key_last4": key_last4,
                "livemode": livemode,
                "created_at": created_at,
                "updated_at": updated_at,
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
        provider = check_billing_provider(d.pop("provider"))

        key_last4 = d.pop("key_last4")

        livemode = d.pop("livemode")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        api_url = d.pop("api_url", UNSET)

        _collection = d.pop("collection", UNSET)
        collection: BillingConnectionResponseCollection | Unset
        if isinstance(_collection, Unset):
            collection = UNSET
        else:
            collection = check_billing_connection_response_collection(_collection)

        days_until_due = d.pop("days_until_due", UNSET)

        automatic_tax = d.pop("automatic_tax", UNSET)

        billing_connection_response = cls(
            provider=provider,
            key_last4=key_last4,
            livemode=livemode,
            created_at=created_at,
            updated_at=updated_at,
            api_url=api_url,
            collection=collection,
            days_until_due=days_until_due,
            automatic_tax=automatic_tax,
        )

        billing_connection_response.additional_properties = d
        return billing_connection_response

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
