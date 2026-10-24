from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="SetPasswordRequest")


@_attrs_define
class SetPasswordRequest:
    """New password for the authenticated account. Lets OAuth-only customers opt into password login, and customers who
    already have a password replace it.

    """

    password: str
    csrf_token: str
    """Double-submit token from `GET /v1/auth/csrf?action=set_password`;
    must equal the `faas_csrf` cookie that call set.
    """
    current_password: str | Unset = UNSET
    """Required when the account already has a password and the
    session carries no step-up from the last 5 minutes
    (ADR-140). Ignored for OAuth-only accounts.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        password = self.password

        csrf_token = self.csrf_token

        current_password = self.current_password

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "password": password,
                "csrf_token": csrf_token,
            }
        )
        if current_password is not UNSET:
            field_dict["current_password"] = current_password

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        password = d.pop("password")

        csrf_token = d.pop("csrf_token")

        current_password = d.pop("current_password", UNSET)

        set_password_request = cls(
            password=password,
            csrf_token=csrf_token,
            current_password=current_password,
        )

        set_password_request.additional_properties = d
        return set_password_request

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
