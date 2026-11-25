from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.consumer_credit_purchase_response_status import (
    ConsumerCreditPurchaseResponseStatus,
    check_consumer_credit_purchase_response_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ConsumerCreditPurchaseResponse")


@_attrs_define
class ConsumerCreditPurchaseResponse:
    """The saved purchase terms, current checkout state and verified credit receipt."""

    id: UUID
    pack_id: str
    currency: str
    amount_millicents: int
    status: ConsumerCreditPurchaseResponseStatus
    expires_at: datetime.datetime
    checkout_url: str | Unset = UNSET
    """Stripe Checkout URL, returned when starting or resuming an open session."""
    credit_grant_id: UUID | Unset = UNSET
    """The prepaid credit grant once Stripe payment is confirmed."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        pack_id = self.pack_id

        currency = self.currency

        amount_millicents = self.amount_millicents

        status: str = self.status

        expires_at = self.expires_at.isoformat()

        checkout_url = self.checkout_url

        credit_grant_id: str | Unset = UNSET
        if not isinstance(self.credit_grant_id, Unset):
            credit_grant_id = str(self.credit_grant_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "pack_id": pack_id,
                "currency": currency,
                "amount_millicents": amount_millicents,
                "status": status,
                "expires_at": expires_at,
            }
        )
        if checkout_url is not UNSET:
            field_dict["checkout_url"] = checkout_url
        if credit_grant_id is not UNSET:
            field_dict["credit_grant_id"] = credit_grant_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        pack_id = d.pop("pack_id")

        currency = d.pop("currency")

        amount_millicents = d.pop("amount_millicents")

        status = check_consumer_credit_purchase_response_status(d.pop("status"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        checkout_url = d.pop("checkout_url", UNSET)

        _credit_grant_id = d.pop("credit_grant_id", UNSET)
        credit_grant_id: UUID | Unset
        if isinstance(_credit_grant_id, Unset):
            credit_grant_id = UNSET
        else:
            credit_grant_id = UUID(_credit_grant_id)

        consumer_credit_purchase_response = cls(
            id=id,
            pack_id=pack_id,
            currency=currency,
            amount_millicents=amount_millicents,
            status=status,
            expires_at=expires_at,
            checkout_url=checkout_url,
            credit_grant_id=credit_grant_id,
        )

        consumer_credit_purchase_response.additional_properties = d
        return consumer_credit_purchase_response

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
