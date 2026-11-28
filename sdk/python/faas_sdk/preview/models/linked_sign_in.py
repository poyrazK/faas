from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.linked_sign_in_provider import LinkedSignInProvider, check_linked_sign_in_provider

T = TypeVar("T", bound="LinkedSignIn")


@_attrs_define
class LinkedSignIn:
    """A Google or GitHub identity linked to the account."""

    provider: LinkedSignInProvider
    email: str
    linked_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        provider: str = self.provider

        email = self.email

        linked_at = self.linked_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "provider": provider,
                "email": email,
                "linked_at": linked_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        provider = check_linked_sign_in_provider(d.pop("provider"))

        email = d.pop("email")

        linked_at = datetime.datetime.fromisoformat(d.pop("linked_at"))

        linked_sign_in = cls(
            provider=provider,
            email=email,
            linked_at=linked_at,
        )

        linked_sign_in.additional_properties = d
        return linked_sign_in

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
