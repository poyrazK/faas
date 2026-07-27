from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="MFAVerifyResponse")


@_attrs_define
class MFAVerifyResponse:
    """Empty success body. Same semantics as /confirm response."""

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        mfa_verify_response = cls()

        return mfa_verify_response
