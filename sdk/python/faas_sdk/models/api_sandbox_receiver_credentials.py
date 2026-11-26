from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="APISandboxReceiverCredentials")


@_attrs_define
class APISandboxReceiverCredentials:
    signing_secret: str
    outcome_token: str
    expires_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        signing_secret = self.signing_secret

        outcome_token = self.outcome_token

        expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "signing_secret": signing_secret,
                "outcome_token": outcome_token,
                "expires_at": expires_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        signing_secret = d.pop("signing_secret")

        outcome_token = d.pop("outcome_token")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        api_sandbox_receiver_credentials = cls(
            signing_secret=signing_secret,
            outcome_token=outcome_token,
            expires_at=expires_at,
        )

        api_sandbox_receiver_credentials.additional_properties = d
        return api_sandbox_receiver_credentials

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
