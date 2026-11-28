from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.account_risk_link_signal import AccountRiskLinkSignal, check_account_risk_link_signal
from ..types import UNSET, Unset

T = TypeVar("T", bound="HeldLinkReview")


@_attrs_define
class HeldLinkReview:
    """A held-account link with the new account's sign-up country and email domain."""

    account_id: str
    held_account_id: str
    held_reason: str
    signal: AccountRiskLinkSignal
    detected_at: datetime.datetime
    disposable_email: bool
    held_org_id: str | Unset = UNSET
    country: str | Unset = UNSET
    email_domain: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        account_id = self.account_id

        held_account_id = self.held_account_id

        held_reason = self.held_reason

        signal: str = self.signal

        detected_at = self.detected_at.isoformat()

        disposable_email = self.disposable_email

        held_org_id = self.held_org_id

        country = self.country

        email_domain = self.email_domain

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "account_id": account_id,
                "held_account_id": held_account_id,
                "held_reason": held_reason,
                "signal": signal,
                "detected_at": detected_at,
                "disposable_email": disposable_email,
            }
        )
        if held_org_id is not UNSET:
            field_dict["held_org_id"] = held_org_id
        if country is not UNSET:
            field_dict["country"] = country
        if email_domain is not UNSET:
            field_dict["email_domain"] = email_domain

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        account_id = d.pop("account_id")

        held_account_id = d.pop("held_account_id")

        held_reason = d.pop("held_reason")

        signal = check_account_risk_link_signal(d.pop("signal"))

        detected_at = datetime.datetime.fromisoformat(d.pop("detected_at"))

        disposable_email = d.pop("disposable_email")

        held_org_id = d.pop("held_org_id", UNSET)

        country = d.pop("country", UNSET)

        email_domain = d.pop("email_domain", UNSET)

        held_link_review = cls(
            account_id=account_id,
            held_account_id=held_account_id,
            held_reason=held_reason,
            signal=signal,
            detected_at=detected_at,
            disposable_email=disposable_email,
            held_org_id=held_org_id,
            country=country,
            email_domain=email_domain,
        )

        held_link_review.additional_properties = d
        return held_link_review

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
