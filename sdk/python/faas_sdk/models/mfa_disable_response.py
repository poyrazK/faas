from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="MFADisableResponse")


@_attrs_define
class MFADisableResponse:
    """Empty success body. Side effects: mfa_secret_encrypted,
    mfa_recovery_codes_hash, and mfa_enrolled_at are all NULL.
    mfa_required is left as-is so the chokepoints can re-arm.

    """

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        mfa_disable_response = cls()

        return mfa_disable_response
