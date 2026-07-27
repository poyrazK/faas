from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="CustomDomainResponse")


@_attrs_define
class CustomDomainResponse:
    """A custom domain binding: domain string, target app, verification status, and TLS provisioning state."""

    domain: str
    app_id: str
    verified: bool
    challenge_token: None | str | Unset = UNSET
    verified_at: datetime.datetime | None | Unset = UNSET
    txt_record: None | str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        domain = self.domain

        app_id = self.app_id

        verified = self.verified

        challenge_token: None | str | Unset
        if isinstance(self.challenge_token, Unset):
            challenge_token = UNSET
        else:
            challenge_token = self.challenge_token

        verified_at: None | str | Unset
        if isinstance(self.verified_at, Unset):
            verified_at = UNSET
        elif isinstance(self.verified_at, datetime.datetime):
            verified_at = self.verified_at.isoformat()
        else:
            verified_at = self.verified_at

        txt_record: None | str | Unset
        if isinstance(self.txt_record, Unset):
            txt_record = UNSET
        else:
            txt_record = self.txt_record

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "domain": domain,
                "app_id": app_id,
                "verified": verified,
            }
        )
        if challenge_token is not UNSET:
            field_dict["challenge_token"] = challenge_token
        if verified_at is not UNSET:
            field_dict["verified_at"] = verified_at
        if txt_record is not UNSET:
            field_dict["txt_record"] = txt_record

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        domain = d.pop("domain")

        app_id = d.pop("app_id")

        verified = d.pop("verified")

        def _parse_challenge_token(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        challenge_token = _parse_challenge_token(d.pop("challenge_token", UNSET))

        def _parse_verified_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                verified_at_type_0 = datetime.datetime.fromisoformat(data)

                return verified_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        verified_at = _parse_verified_at(d.pop("verified_at", UNSET))

        def _parse_txt_record(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        txt_record = _parse_txt_record(d.pop("txt_record", UNSET))

        custom_domain_response = cls(
            domain=domain,
            app_id=app_id,
            verified=verified,
            challenge_token=challenge_token,
            verified_at=verified_at,
            txt_record=txt_record,
        )

        custom_domain_response.additional_properties = d
        return custom_domain_response

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
