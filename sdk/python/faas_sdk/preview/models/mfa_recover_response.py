from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="MFARecoverResponse")


@_attrs_define
class MFARecoverResponse:
    """Empty success body. Side effects: the matching hash is
    removed from `mfa_recovery_codes_hash`; the session cookie
    is re-issued without `mfa_pending`.

    """

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        mfa_recover_response = cls()

        return mfa_recover_response
