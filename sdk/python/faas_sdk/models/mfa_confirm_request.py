from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="MFAConfirmRequest")


@_attrs_define
class MFAConfirmRequest:
    """Body for /confirm — a single 6-digit TOTP code."""

    totp: str
    """ 6-digit rotating code from the authenticator app. """
    csrf_token: str | Unset = UNSET
    """ Dashboard CSRF token. The dashboard sets a
    `faas_csrf` cookie on every authenticated response; the
    same value rides in this JSON-body field so
    `pkg/middleware.VerifyAuthenticated` can compare them.
    The token is opaque to the wire (sealed session
    reference); size matches the cookie value.
     """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        totp = self.totp

        csrf_token = self.csrf_token

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "totp": totp,
            }
        )
        if csrf_token is not UNSET:
            field_dict["csrf_token"] = csrf_token

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        totp = d.pop("totp")

        csrf_token = d.pop("csrf_token", UNSET)

        mfa_confirm_request = cls(
            totp=totp,
            csrf_token=csrf_token,
        )

        mfa_confirm_request.additional_properties = d
        return mfa_confirm_request

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
