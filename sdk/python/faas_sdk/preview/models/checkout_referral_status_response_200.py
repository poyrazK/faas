from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.checkout_referral_status_response_200_reason import (
    CheckoutReferralStatusResponse200Reason,
    check_checkout_referral_status_response_200_reason,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="CheckoutReferralStatusResponse200")


@_attrs_define
class CheckoutReferralStatusResponse200:
    eligible: bool
    reason: CheckoutReferralStatusResponse200Reason | Unset = UNSET
    code: str | Unset = UNSET
    """The already saved referral code; no referrer contact details are returned."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        eligible = self.eligible

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        code = self.code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "eligible": eligible,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if code is not UNSET:
            field_dict["code"] = code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        eligible = d.pop("eligible")

        _reason = d.pop("reason", UNSET)
        reason: CheckoutReferralStatusResponse200Reason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_checkout_referral_status_response_200_reason(_reason)

        code = d.pop("code", UNSET)

        checkout_referral_status_response_200 = cls(
            eligible=eligible,
            reason=reason,
            code=code,
        )

        checkout_referral_status_response_200.additional_properties = d
        return checkout_referral_status_response_200

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
