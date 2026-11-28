from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="AccountEmailChangeRequest")


@_attrs_define
class AccountEmailChangeRequest:
    """Body for `POST /v1/account/email`."""

    email: str
    current_password: str | Unset = UNSET
    """Required when the account has a password and no two-factor authentication."""
    csrf_token: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        email = self.email

        current_password = self.current_password

        csrf_token = self.csrf_token

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "email": email,
            }
        )
        if current_password is not UNSET:
            field_dict["current_password"] = current_password
        if csrf_token is not UNSET:
            field_dict["csrf_token"] = csrf_token

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        email = d.pop("email")

        current_password = d.pop("current_password", UNSET)

        csrf_token = d.pop("csrf_token", UNSET)

        account_email_change_request = cls(
            email=email,
            current_password=current_password,
            csrf_token=csrf_token,
        )

        account_email_change_request.additional_properties = d
        return account_email_change_request

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
