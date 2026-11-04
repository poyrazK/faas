from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.api_consumer_credit_response_status import (
    APIConsumerCreditResponseStatus,
    check_api_consumer_credit_response_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="APIConsumerCreditResponse")


@_attrs_define
class APIConsumerCreditResponse:
    """One prepaid credit grant."""

    id: UUID
    consumer_id: UUID
    currency: str
    amount_millicents: int
    remaining_millicents: int
    status: APIConsumerCreditResponseStatus
    created_at: datetime.datetime
    note: str | Unset = UNSET
    expires_at: datetime.datetime | Unset = UNSET
    voided_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        consumer_id = str(self.consumer_id)

        currency = self.currency

        amount_millicents = self.amount_millicents

        remaining_millicents = self.remaining_millicents

        status: str = self.status

        created_at = self.created_at.isoformat()

        note = self.note

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        voided_at: str | Unset = UNSET
        if not isinstance(self.voided_at, Unset):
            voided_at = self.voided_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "consumer_id": consumer_id,
                "currency": currency,
                "amount_millicents": amount_millicents,
                "remaining_millicents": remaining_millicents,
                "status": status,
                "created_at": created_at,
            }
        )
        if note is not UNSET:
            field_dict["note"] = note
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at
        if voided_at is not UNSET:
            field_dict["voided_at"] = voided_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        consumer_id = UUID(d.pop("consumer_id"))

        currency = d.pop("currency")

        amount_millicents = d.pop("amount_millicents")

        remaining_millicents = d.pop("remaining_millicents")

        status = check_api_consumer_credit_response_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        note = d.pop("note", UNSET)

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        _voided_at = d.pop("voided_at", UNSET)
        voided_at: datetime.datetime | Unset
        if isinstance(_voided_at, Unset):
            voided_at = UNSET
        else:
            voided_at = datetime.datetime.fromisoformat(_voided_at)

        api_consumer_credit_response = cls(
            id=id,
            consumer_id=consumer_id,
            currency=currency,
            amount_millicents=amount_millicents,
            remaining_millicents=remaining_millicents,
            status=status,
            created_at=created_at,
            note=note,
            expires_at=expires_at,
            voided_at=voided_at,
        )

        api_consumer_credit_response.additional_properties = d
        return api_consumer_credit_response

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
