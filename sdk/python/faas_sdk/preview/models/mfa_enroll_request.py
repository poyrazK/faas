from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="MFAEnrollRequest")


@_attrs_define
class MFAEnrollRequest:
    """Empty body — the customer brings only their session cookie.
    Kept as a real struct (not `additionalProperties: false`) so
    `{}` decodes cleanly without a "no JSON object" parse error.

    """

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        mfa_enroll_request = cls()

        return mfa_enroll_request
