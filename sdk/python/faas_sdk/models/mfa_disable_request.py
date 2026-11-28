from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="MFADisableRequest")


@_attrs_define
class MFADisableRequest:
    """Body for /disable. Exactly one of `password` or
    `recovery_code` is required — both empty and both set are
    rejected with 400 CodeValidation. Password is verified
    against the existing `account_passwords.hash`; the
    recovery code is consumed (one-time).

    """

    password: str | Unset = UNSET
    recovery_code: str | Unset = UNSET
    csrf_token: str | Unset = UNSET
    """ Dashboard CSRF token for the disable mutation. """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        password = self.password

        recovery_code = self.recovery_code

        csrf_token = self.csrf_token

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if password is not UNSET:
            field_dict["password"] = password
        if recovery_code is not UNSET:
            field_dict["recovery_code"] = recovery_code
        if csrf_token is not UNSET:
            field_dict["csrf_token"] = csrf_token

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        password = d.pop("password", UNSET)

        recovery_code = d.pop("recovery_code", UNSET)

        csrf_token = d.pop("csrf_token", UNSET)

        mfa_disable_request = cls(
            password=password,
            recovery_code=recovery_code,
            csrf_token=csrf_token,
        )

        mfa_disable_request.additional_properties = d
        return mfa_disable_request

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
