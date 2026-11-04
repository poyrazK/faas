from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="CreateConsumerSignupResponse")


@_attrs_define
class CreateConsumerSignupResponse:
    """A started sign-up. Send the customer to checkout_url; keep claim_token to complete it."""

    id: UUID
    checkout_url: str
    claim_token: str
    expires_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        checkout_url = self.checkout_url

        claim_token = self.claim_token

        expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "checkout_url": checkout_url,
                "claim_token": claim_token,
                "expires_at": expires_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        checkout_url = d.pop("checkout_url")

        claim_token = d.pop("claim_token")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        create_consumer_signup_response = cls(
            id=id,
            checkout_url=checkout_url,
            claim_token=claim_token,
            expires_at=expires_at,
        )

        create_consumer_signup_response.additional_properties = d
        return create_consumer_signup_response

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
