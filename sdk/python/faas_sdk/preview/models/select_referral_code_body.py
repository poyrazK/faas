from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="SelectReferralCodeBody")


@_attrs_define
class SelectReferralCodeBody:
    referral_code: str

    def to_dict(self) -> dict[str, Any]:
        referral_code = self.referral_code

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "referral_code": referral_code,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        referral_code = d.pop("referral_code")

        select_referral_code_body = cls(
            referral_code=referral_code,
        )

        return select_referral_code_body
