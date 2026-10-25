from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateAPIConsumerCreditRequest")


@_attrs_define
class CreateAPIConsumerCreditRequest:
    """Prepaid credit for one consumer (ADR-1306)."""

    currency: str
    amount_millicents: int
    """Credit in millicents; 100000 is 1.00."""
    expires_at: datetime.datetime | Unset = UNSET
    """When unused credit lapses; omitted, it never does."""
    note: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        currency = self.currency

        amount_millicents = self.amount_millicents

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        note = self.note

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "currency": currency,
                "amount_millicents": amount_millicents,
            }
        )
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at
        if note is not UNSET:
            field_dict["note"] = note

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        currency = d.pop("currency")

        amount_millicents = d.pop("amount_millicents")

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        note = d.pop("note", UNSET)

        create_api_consumer_credit_request = cls(
            currency=currency,
            amount_millicents=amount_millicents,
            expires_at=expires_at,
            note=note,
        )

        create_api_consumer_credit_request.additional_properties = d
        return create_api_consumer_credit_request

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
