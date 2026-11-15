from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="AccountRiskSignup")


@_attrs_define
class AccountRiskSignup:
    """The account's sign-up record. signup_ip and user_agent only with include_pii, and only for 90 days."""

    method: str
    email_domain: str
    disposable_email: bool
    created_at: datetime.datetime
    signup_ip: str | Unset = UNSET
    user_agent: str | Unset = UNSET
    country: str | Unset = UNSET
    asn: int | Unset = UNSET
    asn_org: str | Unset = UNSET
    reduced_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        method = self.method

        email_domain = self.email_domain

        disposable_email = self.disposable_email

        created_at = self.created_at.isoformat()

        signup_ip = self.signup_ip

        user_agent = self.user_agent

        country = self.country

        asn = self.asn

        asn_org = self.asn_org

        reduced_at: str | Unset = UNSET
        if not isinstance(self.reduced_at, Unset):
            reduced_at = self.reduced_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "method": method,
                "email_domain": email_domain,
                "disposable_email": disposable_email,
                "created_at": created_at,
            }
        )
        if signup_ip is not UNSET:
            field_dict["signup_ip"] = signup_ip
        if user_agent is not UNSET:
            field_dict["user_agent"] = user_agent
        if country is not UNSET:
            field_dict["country"] = country
        if asn is not UNSET:
            field_dict["asn"] = asn
        if asn_org is not UNSET:
            field_dict["asn_org"] = asn_org
        if reduced_at is not UNSET:
            field_dict["reduced_at"] = reduced_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        method = d.pop("method")

        email_domain = d.pop("email_domain")

        disposable_email = d.pop("disposable_email")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        signup_ip = d.pop("signup_ip", UNSET)

        user_agent = d.pop("user_agent", UNSET)

        country = d.pop("country", UNSET)

        asn = d.pop("asn", UNSET)

        asn_org = d.pop("asn_org", UNSET)

        _reduced_at = d.pop("reduced_at", UNSET)
        reduced_at: datetime.datetime | Unset
        if isinstance(_reduced_at, Unset):
            reduced_at = UNSET
        else:
            reduced_at = datetime.datetime.fromisoformat(_reduced_at)

        account_risk_signup = cls(
            method=method,
            email_domain=email_domain,
            disposable_email=disposable_email,
            created_at=created_at,
            signup_ip=signup_ip,
            user_agent=user_agent,
            country=country,
            asn=asn,
            asn_org=asn_org,
            reduced_at=reduced_at,
        )

        account_risk_signup.additional_properties = d
        return account_risk_signup

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
