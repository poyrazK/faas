from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="MFADisableEmailConfirmResponse")


@_attrs_define
class MFADisableEmailConfirmResponse:
    """Empty success body; MFA state is cleared."""

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        mfa_disable_email_confirm_response = cls()

        return mfa_disable_email_confirm_response
