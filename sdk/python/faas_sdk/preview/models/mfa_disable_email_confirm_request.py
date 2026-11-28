from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="MFADisableEmailConfirmRequest")


@_attrs_define
class MFADisableEmailConfirmRequest:
    """Body for consuming the emailed token after the cooldown."""

    token: str
    """Opaque base64url token from the confirmation email."""
    csrf_token: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        token = self.token

        csrf_token = self.csrf_token

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "token": token,
            }
        )
        if csrf_token is not UNSET:
            field_dict["csrf_token"] = csrf_token

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        token = d.pop("token")

        csrf_token = d.pop("csrf_token", UNSET)

        mfa_disable_email_confirm_request = cls(
            token=token,
            csrf_token=csrf_token,
        )

        return mfa_disable_email_confirm_request
