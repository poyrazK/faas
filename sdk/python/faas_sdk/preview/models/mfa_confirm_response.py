from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="MFAConfirmResponse")


@_attrs_define
class MFAConfirmResponse:
    """Empty success body. The meaningful side effect is the
    `Set-Cookie: faas_sid=…` header — re-issued without the
    `mfa_pending` flag — and the stamp of `mfa_enrolled_at`.

    """

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        mfa_confirm_response = cls()

        return mfa_confirm_response
